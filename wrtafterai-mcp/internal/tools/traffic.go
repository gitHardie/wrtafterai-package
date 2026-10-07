// traffic 域：router.realtime_traffic（/proc/net/dev 双采样）+ router.traffic_by_device（nlbwmon）。
package tools

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- router.realtime_traffic ----

type trafficRow struct {
	Iface  string  `json:"iface"`
	RxKBps float64 `json:"rx_kbps"`
	TxKBps float64 `json:"tx_kbps"`
}

type realtimeTrafficOutput struct {
	IntervalSec int          `json:"interval_sec"`
	Active      []trafficRow `json:"active,omitempty"`
	TotalRxKBps float64      `json:"total_rx_kbps"`
	TotalTxKBps float64      `json:"total_tx_kbps"`
	Note        string       `json:"note,omitempty"`
}

type realtimeTrafficInput struct{}

// realtimeTrafficHandler 采样两次（间隔 1s）计算每活跃接口速率。
// 聚合法：/proc/net/dev 计数器差值，非逐设备精确计量。
func realtimeTrafficHandler(ctx context.Context, req *mcp.CallToolRequest, in realtimeTrafficInput) (*mcp.CallToolResult, realtimeTrafficOutput, error) {
	const interval = time.Second
	var out realtimeTrafficOutput

	first, err := backend.NetDev()
	if err != nil {
		return nil, out, errNetDev(err)
	}
	select {
	case <-time.After(interval):
	case <-ctx.Done():
		return nil, out, ctx.Err()
	}
	second, err := backend.NetDev()
	if err != nil {
		return nil, out, errNetDev(err)
	}

	prev := map[string]backend.IfaceCounters{}
	for _, c := range first {
		prev[c.Iface] = c
	}
	for _, c := range second {
		if c.Iface == "lo" {
			continue
		}
		p, ok := prev[c.Iface]
		if !ok {
			continue // 采样中途新出现的接口无基线
		}
		rxD := delta(c.RxBytes, p.RxBytes)
		txD := delta(c.TxBytes, p.TxBytes)
		if rxD == 0 && txD == 0 {
			continue // 非活跃接口不输出
		}
		row := trafficRow{
			Iface:  c.Iface,
			RxKBps: float64(rxD) / 1024 / interval.Seconds(),
			TxKBps: float64(txD) / 1024 / interval.Seconds(),
		}
		out.Active = append(out.Active, row)
		out.TotalRxKBps += row.RxKBps
		out.TotalTxKBps += row.TxKBps
	}
	out.IntervalSec = int(interval.Seconds())
	if len(out.Active) == 0 {
		out.Note = "no active interface traffic during sampling window"
	}
	return nil, out, nil
}

// delta 计数器差值（计数器回绕/重启时返回 0）。
func delta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func errNetDev(err error) error {
	return &simpleError{"读取 /proc/net/dev 失败: " + err.Error()}
}

// ---- router.traffic_by_device ----

type nlbwRow struct {
	Mac   string `json:"mac"`
	Conns uint64 `json:"conns"`
	Rx    uint64 `json:"rx"` // bytes
	Tx    uint64 `json:"tx"` // bytes
}

type devTraffic struct {
	MAC      string  `json:"mac"`
	Hostname string  `json:"hostname,omitempty"`
	Conns    uint64  `json:"conns,omitempty"`
	RxMB     float64 `json:"rx_mb"`
	TxMB     float64 `json:"tx_mb"`
	TotalMB  float64 `json:"total_mb"`
}

type trafficByDeviceOutput struct {
	Count   int          `json:"count,omitempty"`
	Period  string       `json:"period,omitempty"`
	Devices []devTraffic `json:"devices,omitempty"`
	Note    string       `json:"note,omitempty"`
}

type trafficByDeviceInput struct{}

func trafficByDeviceHandler(ctx context.Context, req *mcp.CallToolRequest, in trafficByDeviceInput) (*mcp.CallToolResult, trafficByDeviceOutput, error) {
	var out trafficByDeviceOutput

	var nl struct {
		Devices []nlbwRow `json:"devices"`
		Rows    []nlbwRow `json:"rows"` // nlbwmon ubus 实际字段
		Period  struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"period"`
	}
	if err := backend.UbusCall(ctx, "nlbw", "get", nil, &nl); err != nil {
		return nil, out, errNlbw(err)
	}
	rows := nl.Devices
	if len(rows) == 0 {
		rows = nl.Rows
	}
	if len(rows) == 0 {
		out.Note = "nlbwmon 已响应但无计数数据（可能刚重置统计周期）"
		return nil, out, nil
	}
	if nl.Period.Start > 0 {
		out.Period = time.Unix(nl.Period.Start, 0).UTC().Format("2006-01-02") +
			" ~ " + time.Unix(nl.Period.End, 0).UTC().Format("2006-01-02")
	}

	// 尽力补充主机名（失败不报错）
	hostnames := map[string]string{}
	if leases, err := backend.DHCPLeases(); err == nil {
		for _, l := range leases {
			if l.Hostname != "" && l.Hostname != "*" {
				hostnames[strings.ToLower(l.MAC)] = l.Hostname
			}
		}
	}

	for _, r := range rows {
		d := devTraffic{
			MAC:      strings.ToLower(r.Mac),
			Hostname: hostnames[strings.ToLower(r.Mac)],
			Conns:    r.Conns,
			RxMB:     mb(r.Rx),
			TxMB:     mb(r.Tx),
			TotalMB:  mb(r.Rx + r.Tx),
		}
		out.Devices = append(out.Devices, d)
	}
	sort.Slice(out.Devices, func(i, j int) bool {
		return out.Devices[i].TotalMB > out.Devices[j].TotalMB
	})
	out.Count = len(out.Devices)
	return nil, out, nil
}

func errNlbw(err error) error {
	return &simpleError{
		"未安装 nlbwmon，可在固件中启用（安装 nlbwmon 包并重启）: " + briefErr(err),
	}
}

// RegisterTraffic 注册 traffic 域工具。
func RegisterTraffic(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.realtime_traffic",
		Description: "Sample /proc/net/dev twice (1s apart) and report per-interface rx/tx rate in KB/s (aggregate estimate). Takes ~1s.",
	}, realtimeTrafficHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.traffic_by_device",
		Description: "Per-device traffic totals (rx/tx MB) from nlbwmon. Requires nlbwmon installed; returns install guidance otherwise.",
	}, trafficByDeviceHandler)
}
