// network 域：router.network_interfaces / router.wan_status。
// 数据源：ubus network.interface dump/status + /proc/net/dev 字节计数合并。
package tools

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- ubus network.interface 结构 ----

type netRoute struct {
	Target      string `json:"target"`
	Nexthop     string `json:"nexthop"`
	Destination string `json:"destination"`
}

type netIface struct {
	Interface string   `json:"interface"`
	Up        bool     `json:"up"`
	Proto     string   `json:"proto"`
	Uptime    int64    `json:"uptime"`
	Device    string   `json:"device"`
	L3Device  string   `json:"l3_device"`
	Address   []string `json:"address"` // 部分固件直接给字符串数组
	IPv4Addr  []struct {
		Address string `json:"address"`
		Mask    int    `json:"mask"`
	} `json:"ipv4-address"`
	Route     []netRoute     `json:"route"`
	DnsServer []string       `json:"dns-server"`
	Data      map[string]any `json:"data"`
}

// ifaceListOutput / router.network_interfaces
type ifaceListOutput struct {
	Interfaces []ifaceOut `json:"interfaces,omitempty"`
	Note       string     `json:"note,omitempty"`
}

type ifaceOut struct {
	Name    string   `json:"name,omitempty"` // 逻辑接口（lan/wan/...）
	Device  string   `json:"device,omitempty"`
	Up      bool     `json:"up"`
	Proto   string   `json:"proto,omitempty"`
	IPv4    []string `json:"ipv4,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	RxMB    float64  `json:"rx_mb,omitempty"`
	TxMB    float64  `json:"tx_mb,omitempty"`
}

type networkInterfacesInput struct{}

func networkInterfacesHandler(ctx context.Context, req *mcp.CallToolRequest, in networkInterfacesInput) (*mcp.CallToolResult, ifaceListOutput, error) {
	var out ifaceListOutput

	// 物理口字节计数（几乎总可用）
	counters := map[string]backend.IfaceCounters{}
	if nc, err := backend.NetDev(); err == nil {
		for _, c := range nc {
			if c.Iface == "lo" {
				continue
			}
			counters[c.Iface] = c
		}
	}

	byDev := func(dev string) (backend.IfaceCounters, bool) {
		c, ok := counters[dev]
		return c, ok
	}
	fillCounters := func(o *ifaceOut, dev string) {
		if c, ok := byDev(dev); ok {
			o.RxMB = mb(c.RxBytes)
			o.TxMB = mb(c.TxBytes)
		}
	}

	var dump struct {
		Interface []netIface `json:"interface"`
	}
	if err := backend.UbusCall(ctx, "network.interface", "dump", nil, &dump); err != nil {
		// 降级：ubus 不可用时仅输出物理口字节计数
		out.Note = "ubus network.interface dump failed (" + briefErr(err) + "), showing physical device counters only"
		for name, c := range counters {
			out.Interfaces = append(out.Interfaces, ifaceOut{
				Device: name, RxMB: mb(c.RxBytes), TxMB: mb(c.TxBytes),
			})
		}
		sortIfaceOut(out.Interfaces)
		return nil, out, nil
	}

	usedDev := map[string]bool{}
	for _, it := range dump.Interface {
		dev := it.Device
		if dev == "" {
			dev = it.L3Device
		}
		if dev == "" {
			if s, ok := it.Data["device"].(string); ok {
				dev = s
			}
		}
		if dev != "" {
			usedDev[dev] = true
		}
		o := ifaceOut{
			Name:   it.Interface,
			Device: dev,
			Up:     it.Up,
			Proto:  it.Proto,
			DNS:    it.DnsServer,
		}
		o.IPv4 = append(o.IPv4, it.Address...)
		for _, a := range it.IPv4Addr {
			if a.Address != "" {
				if a.Mask > 0 {
					o.IPv4 = append(o.IPv4, a.Address+"/"+strconv.Itoa(a.Mask))
				} else {
					o.IPv4 = append(o.IPv4, a.Address)
				}
			}
		}
		for _, r := range it.Route {
			gh := r.Nexthop
			if gh == "" {
				gh = r.Destination
			}
			if r.Target == "0.0.0.0" && gh != "" && o.Gateway == "" {
				o.Gateway = gh
			}
			if r.Target == "::" && gh != "" {
				// IPv6 默认路由信息并入 gateway 字段（带标记），IPv4 优先
				if o.Gateway == "" {
					o.Gateway = gh
				}
			}
		}
		fillCounters(&o, dev)
		out.Interfaces = append(out.Interfaces, o)
	}

	// 未被逻辑接口引用的物理口（桥成员/独立口）补充输出
	for name, c := range counters {
		if usedDev[name] {
			continue
		}
		out.Interfaces = append(out.Interfaces, ifaceOut{
			Device: name, RxMB: mb(c.RxBytes), TxMB: mb(c.TxBytes),
		})
	}
	sortIfaceOut(out.Interfaces)
	return nil, out, nil
}

func sortIfaceOut(list []ifaceOut) {
	sort.Slice(list, func(i, j int) bool {
		// 逻辑接口在前（有名），物理口在后；各自按名字排序
		ai, aj := list[i].Name != "", list[j].Name != ""
		if ai != aj {
			return ai
		}
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].Device < list[j].Device
	})
}

// ---- router.wan_status ----

type wanState struct {
	Up      bool     `json:"up"`
	Proto   string   `json:"proto,omitempty"`
	Uptime  int64    `json:"uptime_sec,omitempty"`
	IP      string   `json:"ip,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	Note    string   `json:"note,omitempty"`
}

type wanStatusOutput struct {
	Wan  wanState `json:"wan"`
	Wan6 wanState `json:"wan6"`
}

type wanStatusInput struct{}

func wanStatusHandler(ctx context.Context, req *mcp.CallToolRequest, in wanStatusInput) (*mcp.CallToolResult, wanStatusOutput, error) {
	var out wanStatusOutput
	out.Wan = fetchWanState(ctx, "network.interface.wan")
	out.Wan6 = fetchWanState(ctx, "network.interface.wan6")
	return nil, out, nil
}

func fetchWanState(ctx context.Context, obj string) wanState {
	var st netIface
	if err := backend.UbusCall(ctx, obj, "status", nil, &st); err != nil {
		// wan 不存在/未配置时优雅降级
		return wanState{Up: false, Note: "unavailable: " + briefErr(err)}
	}
	w := wanState{Up: st.Up, Proto: st.Proto, Uptime: st.Uptime}
	if len(st.IPv4Addr) > 0 {
		w.IP = st.IPv4Addr[0].Address
	}
	if w.IP == "" && len(st.Address) > 0 {
		w.IP = st.Address[0]
	}
	for _, r := range st.Route {
		gh := r.Nexthop
		if gh == "" {
			gh = r.Destination
		}
		if r.Target == "0.0.0.0" && gh != "" {
			w.Gateway = gh
			break
		}
	}
	w.DNS = st.DnsServer
	return w
}

// ---- 共用 helpers ----

// briefErr 把长错误压缩为最后一段（如 "Object not found"）。
func briefErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > 120 {
		s = s[:120]
	}
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

func mb(b uint64) float64 { return float64(b) / 1024 / 1024 }

// RegisterNetwork 注册 network 域工具。
func RegisterNetwork(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.network_interfaces",
		Description: "List all network interfaces: logical name, physical device, up/proto, IPv4, gateway, DNS and rx/tx counters (merged from ubus network dump and /proc/net/dev).",
	}, networkInterfacesHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.wan_status",
		Description: "WAN connectivity status for wan and wan6: up, proto, uptime, IP, gateway, DNS. Degrades gracefully when wan is not configured.",
	}, wanStatusHandler)
}
