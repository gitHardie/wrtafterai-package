// devices 域：router.wireless_clients / router.connected_devices / router.connection_stats / router.dhcp_leases。
package tools

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- router.wireless_clients ----

type iwDevice struct {
	Name string
	SSID string
}

type wirelessClient struct {
	MAC    string  `json:"mac"`
	Device string  `json:"device,omitempty"` // wlan0 / wlan1 ...
	SSID   string  `json:"ssid,omitempty"`
	Signal int     `json:"signal_dbm"`
	Noise  int     `json:"noise_dbm,omitempty"`
	RxMbps float64 `json:"rx_mbps,omitempty"`
	TxMbps float64 `json:"tx_mbps,omitempty"`
}

type wirelessClientsOutput struct {
	Count   int              `json:"count"`
	Clients []wirelessClient `json:"clients,omitempty"`
	Note    string           `json:"note,omitempty"`
}

type wirelessClientsInput struct{}

func wirelessClientsHandler(ctx context.Context, req *mcp.CallToolRequest, in wirelessClientsInput) (*mcp.CallToolResult, wirelessClientsOutput, error) {
	var out wirelessClientsOutput

	// 1. 列出无线设备（iwinfo 无参输出：每个 wlan*/phy 接口一个状态块）
	raw, err := backend.Run(ctx, 5*time.Second, "iwinfo")
	if err != nil {
		return nil, out, errNoWireless(err)
	}
	devs := iwinfoDevices(raw)
	if len(devs) == 0 {
		out.Note = "no wireless interfaces found by iwinfo"
		return nil, out, nil
	}

	// 2. 每设备取关联客户端列表
	for _, d := range devs {
		var assoc struct {
			Results []struct {
				Mac    string `json:"mac"`
				Signal int    `json:"signal"`
				Noise  int    `json:"noise"`
				RxRate uint64 `json:"rx_rate"` // 部分版本直接给 kbit/s
				TxRate uint64 `json:"tx_rate"`
				Rx     struct {
					Rate uint64 `json:"rate"` // kbit/s
				} `json:"rx"`
				Tx struct {
					Rate uint64 `json:"rate"`
				} `json:"tx"`
			} `json:"results"`
		}
		err := backend.UbusCall(ctx, "iwinfo", "assoclist", map[string]string{"device": d.Name}, &assoc)
		if err == nil && len(assoc.Results) > 0 {
			for _, r := range assoc.Results {
				c := wirelessClient{
					MAC:    strings.ToLower(r.Mac),
					Device: d.Name,
					SSID:   d.SSID,
					Signal: r.Signal,
					Noise:  r.Noise,
				}
				if r.Rx.Rate > 0 {
					c.RxMbps = float64(r.Rx.Rate) / 1000
				} else {
					c.RxMbps = float64(r.RxRate) / 1000
				}
				if r.Tx.Rate > 0 {
					c.TxMbps = float64(r.Tx.Rate) / 1000
				} else {
					c.TxMbps = float64(r.TxRate) / 1000
				}
				out.Clients = append(out.Clients, c)
			}
			continue
		}
		// fallback：iwinfo <dev> assoclist 文本解析
		if txt, err2 := backend.Run(ctx, 5*time.Second, "iwinfo", d.Name, "assoclist"); err2 == nil {
			for _, c := range parseIwinfoAssoc(txt) {
				c.Device = d.Name
				c.SSID = d.SSID
				out.Clients = append(out.Clients, c)
			}
		}
	}
	out.Count = len(out.Clients)
	return nil, out, nil
}

func errNoWireless(err error) error {
	return err
}

// iwinfoDevices 解析 iwinfo 无参输出中的接口行（wlan*/phy* 开头）。
func iwinfoDevices(raw string) []iwDevice {
	var devs []iwDevice
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		name := f[0]
		if !strings.HasPrefix(name, "wlan") && !strings.HasPrefix(name, "phy") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		d := iwDevice{Name: name}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "ESSID:" {
				d.SSID = strings.Trim(f[i+1], `"`)
				break
			}
		}
		devs = append(devs, d)
	}
	return devs
}

// parseIwinfoAssoc 解析 "iwinfo <dev> assoclist" 文本（ubus 不可用时的 fallback）。
// 行格式示例:
//
//	AA:BB:CC:DD:EE:FF  -52 dBm / -95 dBm (SNR 43)  120 ms ago
//	        RX: 130.0 MBit/s, MCS 14, 20 MHz           1032 Pkts.
//	        TX: 260.0 MBit/s, MCS 15, 40 MHz           2048 Pkts.
func parseIwinfoAssoc(text string) []wirelessClient {
	var out []wirelessClient
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if isMAC(f[0]) {
			c := wirelessClient{MAC: strings.ToLower(f[0])}
			if v, err := strconv.ParseFloat(f[1], 64); err == nil {
				c.Signal = int(v)
			}
			// "-52 dBm / -95 dBm": signal f[1], noise f[4]
			for i := 2; i+2 < len(f); i++ {
				if f[i] == "/" {
					if v, err := strconv.ParseFloat(f[i+1], 64); err == nil {
						c.Noise = int(v)
					}
					break
				}
			}
			out = append(out, c)
			continue
		}
		if len(out) == 0 {
			continue
		}
		last := &out[len(out)-1]
		switch f[0] {
		case "RX:", "TX:":
			// "RX: 130.0 MBit/s" 或 "RX: 130.0 MBit/s, MCS ..."
			if len(f) >= 3 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil && strings.HasPrefix(f[2], "MBit/s") {
					if f[0] == "RX:" {
						last.RxMbps = v
					} else {
						last.TxMbps = v
					}
				}
			}
		}
	}
	return out
}

func isMAC(s string) bool {
	if len(s) != 17 || strings.Count(s, ":") != 5 {
		return false
	}
	for i := 0; i < 17; i++ {
		c := s[i]
		if (i+1)%3 == 0 {
			if c != ':' {
				return false
			}
		} else if !isHex(c) {
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// ---- router.connected_devices ----

type deviceOut struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Dev      string `json:"dev,omitempty"`  // 物理口/桥（br-lan 等）
	Zone     string `json:"zone,omitempty"` // 粗略归组 lan/wan
	Online   bool   `json:"online"`         // ARP 表中为有效邻居（flags 0x2 / REACHABLE）
}

type connectedDevicesOutput struct {
	Count   int         `json:"count"`
	Devices []deviceOut `json:"devices,omitempty"`
	Note    string      `json:"note,omitempty"`
}

type connectedDevicesInput struct{}

func connectedDevicesHandler(ctx context.Context, req *mcp.CallToolRequest, in connectedDevicesInput) (*mcp.CallToolResult, connectedDevicesOutput, error) {
	var out connectedDevicesOutput

	leases, leaseErr := backend.DHCPLeases()
	arps, arpErr := backend.ARPTable()
	if leaseErr != nil && arpErr != nil {
		return nil, out, errBothUnavailable(leaseErr, arpErr)
	}
	if leaseErr != nil {
		out.Note = "dhcp.leases unavailable: " + briefErr(leaseErr)
	}
	if arpErr != nil {
		out.Note = strings.TrimSpace(out.Note + " /proc/net/arp unavailable: " + briefErr(arpErr))
	}

	m := map[string]*deviceOut{} // key: IP
	for _, l := range leases {
		m[l.IP] = &deviceOut{IP: l.IP, MAC: l.MAC, Hostname: l.Hostname, Zone: "lan"}
	}
	for _, a := range arps {
		d, ok := m[a.IP]
		if !ok {
			d = &deviceOut{IP: a.IP}
			m[a.IP] = d
		}
		if d.MAC == "" {
			d.MAC = a.MAC
		}
		if d.Hostname == "" {
			// 尝试从租约里捞 hostname（IP 一致但 MAC 变化等边缘情况）
			for _, l := range leases {
				if l.IP == a.IP && l.Hostname != "" && l.Hostname != "*" {
					d.Hostname = l.Hostname
					break
				}
			}
		}
		d.Dev = a.Dev
		d.Zone = zoneOf(a.Dev, d.Zone == "lan")
		d.Online = a.Online()
	}

	for _, d := range m {
		if d.Zone == "" {
			d.Zone = zoneOf(d.Dev, false)
		}
		out.Devices = append(out.Devices, *d)
	}
	sortDevices(out.Devices)
	out.Count = len(out.Devices)
	return nil, out, nil
}

// zoneOf 通过接口名粗略归到 lan/wan：br-lan → lan；wan/pppoe → wan；其余原样。
func zoneOf(dev string, fallbackLan bool) string {
	d := strings.ToLower(dev)
	switch {
	case strings.HasPrefix(d, "br-lan"), strings.HasPrefix(d, "lan"):
		return "lan"
	case strings.HasPrefix(d, "wan"), strings.HasPrefix(d, "pppoe"), strings.HasPrefix(d, "wwan"):
		return "wan"
	}
	if dev == "" && fallbackLan {
		return "lan"
	}
	return dev
}

func sortDevices(list []deviceOut) {
	sort.Slice(list, func(i, j int) bool {
		a, ea := netip.ParseAddr(list[i].IP)
		b, eb := netip.ParseAddr(list[j].IP)
		if ea == nil && eb == nil {
			return a.Less(b)
		}
		return list[i].IP < list[j].IP
	})
}

func errBothUnavailable(a, b error) error {
	return errJoin("both dhcp.leases and /proc/net/arp unavailable: ", a, b)
}

func errJoin(prefix string, errs ...error) error {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		if e != nil {
			msgs = append(msgs, e.Error())
		}
	}
	return &simpleError{prefix + strings.Join(msgs, "; ")}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// ---- router.connection_stats ----

type connStatsOutput struct {
	Total       int `json:"total"`
	TCP         int `json:"tcp"`
	UDP         int `json:"udp"`
	ICMP        int `json:"icmp"`
	Other       int `json:"other"`
	Established int `json:"established"`
	MaxEntries  int `json:"max_entries,omitempty"`
}

type connectionStatsInput struct{}

func connectionStatsHandler(ctx context.Context, req *mcp.CallToolRequest, in connectionStatsInput) (*mcp.CallToolResult, connStatsOutput, error) {
	var out connStatsOutput
	st, err := backend.GetConntrackStats()
	if err != nil {
		return nil, out, errConntrack(err)
	}
	out.Total = st.Total
	out.TCP = st.ByProto["tcp"]
	out.UDP = st.ByProto["udp"]
	out.ICMP = st.ByProto["icmp"] + st.ByProto["icmpv6"]
	out.Established = st.Established
	out.MaxEntries = st.MaxEntries
	out.Other = st.Total - out.TCP - out.UDP - out.ICMP
	if out.Other < 0 {
		out.Other = 0
	}
	return nil, out, nil
}

func errConntrack(err error) error {
	return &simpleError{"读取连接跟踪失败（内核可能未启用 nf_conntrack）: " + err.Error()}
}

// ---- router.dhcp_leases ----

type leaseRow struct {
	Hostname  string `json:"hostname,omitempty"`
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Expires   string `json:"expires"` // RFC3339 / "never" / "expired"
	RemainSec int64  `json:"remain_sec,omitempty"`
}

type dhcpLeasesOutput struct {
	Count  int        `json:"count"`
	Leases []leaseRow `json:"leases,omitempty"`
}

type dhcpLeasesInput struct{}

func dhcpLeasesHandler(ctx context.Context, req *mcp.CallToolRequest, in dhcpLeasesInput) (*mcp.CallToolResult, dhcpLeasesOutput, error) {
	var out dhcpLeasesOutput
	leases, err := backend.DHCPLeases()
	if err != nil {
		return nil, out, errDhcp(err)
	}
	now := time.Now().Unix()
	for _, l := range leases {
		row := leaseRow{Hostname: l.Hostname, IP: l.IP, MAC: l.MAC}
		switch {
		case l.Expiry <= 0:
			row.Expires = "never"
		case l.Expiry < now:
			row.Expires = "expired"
		default:
			row.Expires = time.Unix(l.Expiry, 0).UTC().Format(time.RFC3339)
			row.RemainSec = l.Expiry - now
		}
		out.Leases = append(out.Leases, row)
	}
	out.Count = len(out.Leases)
	return nil, out, nil
}

func errDhcp(err error) error {
	return &simpleError{"读取 DHCP 租约失败: " + err.Error()}
}

// RegisterDevices 注册 devices 域工具。
func RegisterDevices(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.wireless_clients",
		Description: "List Wi-Fi clients per wireless interface: MAC, SSID/device, signal, rx/tx rate. Uses ubus iwinfo assoclist with iwinfo text fallback.",
	}, wirelessClientsHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.connected_devices",
		Description: "List known devices merged from DHCP leases and ARP table: IP, MAC, hostname, interface zone (lan/wan) and online status.",
	}, connectedDevicesHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.connection_stats",
		Description: "Connection tracking totals: TCP/UDP/ICMP/other/total, established count and conntrack table limit.",
	}, connectionStatsHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.dhcp_leases",
		Description: "List active DHCP leases: hostname, IP, MAC and expiry time.",
	}, dhcpLeasesHandler)
}
