// diagnostics 域：router.public_ip / router.dns_resolve / router.dns_config / router.routes / router.ping。
// 所有外部命令经 backend.Run（参数数组，不经 shell），入参走白名单校验。
package tools

import (
	"context"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// lookPath 抽成包级 var 便于单测注入（生产路径为 exec.LookPath）。
var lookPath = exec.LookPath

// findTool 返回命令绝对路径，找不到返回空串。
func findTool(name string) string {
	p, _ := lookPath(name)
	return p
}

// hostRe 域名/IPv4 白名单：字母数字开头，仅含字母数字点横线下划线。
var hostRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validHost 校验域名或 IPv4 主机名（长度 ≤253）。
func validHost(s string) bool {
	return s != "" && len(s) <= 253 && hostRe.MatchString(s)
}

// ---- router.public_ip ----

type publicIPOutput struct {
	WanIP      string `json:"wan_ip,omitempty"`
	PublicIP   string `json:"public_ip,omitempty"`
	BehindNAT  bool   `json:"behind_nat"`
	NATType    string `json:"nat_type,omitempty"`
	MatchesWan bool   `json:"matches_wan,omitempty"`
	Note       string `json:"note,omitempty"`
}

type publicIPInput struct{}

func publicIPHandler(ctx context.Context, req *mcp.CallToolRequest, in publicIPInput) (*mcp.CallToolResult, publicIPOutput, error) {
	var out publicIPOutput

	// WAN IP 复用 network.go 的 wan 状态
	wan := fetchWanState(ctx, "network.interface.wan")
	out.WanIP = wan.IP

	// 出口公网 IP：curl -4 ipify（curl 缺失或失败只降级不报错）
	if findTool("curl") == "" {
		out.Note = "curl 不可用，仅返回 WAN IP"
	} else {
		pub, err := backend.Run(ctx, 10*time.Second, "curl", "-4", "-s", "--max-time", "5", "https://api.ipify.org")
		if err != nil {
			out.Note = "出口 IP 查询失败（外网可能不可达）: " + briefErr(err)
		} else if p := strings.TrimSpace(pub); p != "" {
			out.PublicIP = p
		} else {
			out.Note = "出口 IP 查询返回空（外网可能不可达）"
		}
	}

	out.BehindNAT, out.NATType, out.MatchesWan, out.Note = classifyPublicIP(out.WanIP, out.PublicIP, out.Note)
	return nil, out, nil
}

// classifyPublicIP 纯函数：私网/CGNAT 判断与两 IP 一致性（便于单测）。
func classifyPublicIP(wanIP, publicIP, note string) (behindNAT bool, natType string, matches bool, finalNote string) {
	finalNote = note
	if isPrivateOrCGNAT(wanIP) {
		behindNAT = true
		if isCGNAT(wanIP) {
			natType = "CGNAT (100.64/10)"
		} else {
			natType = "私网 NAT"
		}
	}
	if publicIP == "" {
		return behindNAT, natType, false, finalNote
	}
	matches = publicIP == wanIP
	switch {
	case matches && behindNAT:
		finalNote = joinNote(finalNote, "WAN 为内网/CGNAT 地址，出口公网 IP 已一并给出")
	case !matches && wanIP != "" && !behindNAT:
		finalNote = joinNote(finalNote, "出口 IP 与 WAN IP 不一致（可能存在多 WAN 或上游代理）")
	}
	return behindNAT, natType, matches, finalNote
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// isPrivateOrCGNAT 判断 IPv4 是否私网（10/8、172.16/12、192.168/16）或 CGNAT（100.64/10）。
func isPrivateOrCGNAT(ip string) bool {
	return isPrivateIPv4(ip) || isCGNAT(ip)
}

func isPrivateIPv4(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !a.Is4() {
		return false
	}
	for _, p := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
}

func isCGNAT(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !a.Is4() {
		return false
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// ---- router.dns_resolve ----

type dnsResolveOutput struct {
	Domain  string   `json:"domain"`
	Server  string   `json:"server,omitempty"`
	Answers []string `json:"answers,omitempty"`
	Count   int      `json:"count"`
}

type dnsResolveInput struct {
	Domain string `json:"domain" jsonschema:"domain name to resolve (letters/digits/dots/hyphens)"`
	Server string `json:"server,omitempty" jsonschema:"optional DNS server IP to query instead of system default"`
}

func dnsResolveHandler(ctx context.Context, req *mcp.CallToolRequest, in dnsResolveInput) (*mcp.CallToolResult, dnsResolveOutput, error) {
	var out dnsResolveOutput
	if !validHost(in.Domain) {
		return nil, out, &simpleError{"非法域名: 仅允许字母/数字/点/横线且长度≤253"}
	}
	if in.Server != "" && !validHost(in.Server) {
		return nil, out, &simpleError{"非法 DNS 服务器地址: " + in.Server}
	}

	args := []string{in.Domain}
	if in.Server != "" {
		args = append(args, in.Server)
	}
	text, err := backend.Run(ctx, 10*time.Second, "nslookup", args...)
	out.Domain = in.Domain
	if err != nil {
		excerpt := strings.TrimSpace(text)
		if excerpt == "" {
			excerpt = briefErr(err)
		} else if len(excerpt) > 300 {
			excerpt = excerpt[:300]
		}
		return nil, out, &simpleError{"DNS 解析失败: " + excerpt}
	}
	server, answers := parseNslookup(text)
	out.Server = server
	out.Answers = answers
	out.Count = len(answers)
	if out.Count == 0 {
		return nil, out, &simpleError{"DNS 应答中未解析到任何 Address（上游可能返回 NXDOMAIN）: " + truncateLines(text, 5)}
	}
	return nil, out, nil
}

// parseNslookup 解析 busybox nslookup 输出中的 Server 与 Address 对。
// 兼容新旧两种格式：`Address: 1.2.3.4` 与 `Address 1: 1.2.3.4 host.name`。
func parseNslookup(text string) (server string, answers []string) {
	seen := map[string]bool{}
	inAnswer := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		ls := strings.ToLower(line)
		switch {
		case strings.HasPrefix(ls, "name:"):
			inAnswer = true
		case strings.HasPrefix(ls, "server:") && !inAnswer:
			server = strings.TrimSpace(line[len("Server:"):])
		case strings.HasPrefix(ls, "address") && inAnswer:
			_, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			ip := strings.Fields(val)
			if len(ip) == 0 || ip[0] == "" {
				continue
			}
			if !seen[ip[0]] {
				seen[ip[0]] = true
				answers = append(answers, ip[0])
			}
		}
	}
	return server, answers
}

// ---- router.dns_config ----

type dnsConfigOutput struct {
	ResolvFile      string   `json:"resolv_file,omitempty"`
	ResolvServers   []string `json:"resolv_servers,omitempty"`
	UpstreamServers []string `json:"upstream_servers,omitempty"` // dnsmasq server= 列表
	StaticEntries   []string `json:"static_entries,omitempty"`   // dnsmasq address= 列表
	Noresolv        bool     `json:"noresolv,omitempty"`
	Note            string   `json:"note,omitempty"`
}

type dnsConfigInput struct{}

func dnsConfigHandler(ctx context.Context, req *mcp.CallToolRequest, in dnsConfigInput) (*mcp.CallToolResult, dnsConfigOutput, error) {
	var out dnsConfigOutput
	var notes []string

	// 1. resolv.conf（OpenWrt 新版在 /tmp/resolv.conf.d/，老版直接 /tmp/resolv.conf）
	for _, p := range []string{"/tmp/resolv.conf.d/resolv.conf", "/tmp/resolv.conf"} {
		b, err := procReader(p)
		if err != nil {
			continue
		}
		out.ResolvFile = p
		out.ResolvServers = parseResolvConf(string(b))
		break
	}
	if out.ResolvFile == "" {
		notes = append(notes, "resolv.conf 不可读（dnsmasq 可能未运行）")
	}

	// 2. uci show dhcp：提取 dnsmasq 的 server/address/noresolv
	raw, err := backend.UCIShow(ctx, "dhcp")
	if err != nil {
		notes = append(notes, "uci show dhcp 失败: "+briefErr(err))
	} else {
		secs := parseUCISectionsOf(raw, "dhcp")
		for _, sec := range sortedKeys(secs) {
			m := secs[sec]
			if m[secTypeKey] != "dnsmasq" {
				continue
			}
			out.UpstreamServers = append(out.UpstreamServers, splitUCIValues(m["server"])...)
			out.StaticEntries = append(out.StaticEntries, splitUCIValues(m["address"])...)
			if v := m["noresolv"]; v == "1" || v == "true" {
				out.Noresolv = true
			}
		}
	}

	if out.ResolvServers == nil && out.UpstreamServers == nil && out.StaticEntries == nil && len(notes) > 0 {
		return nil, out, &simpleError{"DNS 配置读取失败: " + strings.Join(notes, "; ")}
	}
	out.Note = strings.Join(notes, "; ")
	return nil, out, nil
}

// parseResolvConf 提取 nameserver 列表。
func parseResolvConf(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}

// ---- router.routes ----

type routesOutput struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6,omitempty"`
	Note string `json:"note,omitempty"`
}

type routesInput struct{}

func routesHandler(ctx context.Context, req *mcp.CallToolRequest, in routesInput) (*mcp.CallToolResult, routesOutput, error) {
	var out routesOutput
	v4, err := backend.Run(ctx, 10*time.Second, "ip", "route")
	if err != nil {
		return nil, out, &simpleError{"ip route 执行失败: " + briefErr(err)}
	}
	out.IPv4 = truncateLines(v4, 100)

	// IPv6 路由失败可忽略（部分固件未启用 ipv6）
	if v6, err := backend.Run(ctx, 10*time.Second, "ip", "-6", "route"); err == nil && strings.TrimSpace(v6) != "" {
		out.IPv6 = truncateLines(v6, 20)
	} else if err != nil {
		out.Note = "IPv6 路由不可用: " + briefErr(err)
	}
	return nil, out, nil
}

// ---- router.ping ----

type pingOutput struct {
	Host        string  `json:"host"`
	Transmitted int     `json:"transmitted"`
	Received    int     `json:"received"`
	LossPct     float64 `json:"loss_pct"`
	MinMs       float64 `json:"min_ms,omitempty"`
	AvgMs       float64 `json:"avg_ms,omitempty"`
	MaxMs       float64 `json:"max_ms,omitempty"`
	Raw         string  `json:"raw,omitempty"`
}

type pingInput struct {
	Host  string `json:"host" jsonschema:"hostname or IPv4 address to ping"`
	Count int    `json:"count,omitempty" jsonschema:"number of pings (1-10, default 4)"`
}

func pingHandler(ctx context.Context, req *mcp.CallToolRequest, in pingInput) (*mcp.CallToolResult, pingOutput, error) {
	var out pingOutput
	if !validHost(in.Host) {
		return nil, out, &simpleError{"非法目标主机: 仅允许域名或 IPv4 且长度≤253"}
	}
	n := in.Count
	if n <= 0 {
		n = 4
	}
	if n > 10 {
		n = 10
	}

	out.Host = in.Host
	text, err := backend.Run(ctx, 30*time.Second, "ping", "-c", strconv.Itoa(n), "-W", "2", in.Host)
	if err != nil {
		// 全部丢包时 busybox ping 以非 0 退出，stdout 摘要已随错误丢失，给出明确提示
		return nil, out, &simpleError{"ping 执行失败（主机不可达或全部丢包）: " + briefErr(err)}
	}
	out.Raw = truncateLines(text, 20)
	tx, rx, loss, minMs, avgMs, maxMs, ok := parsePingOutput(text)
	if !ok {
		return nil, out, &simpleError{"ping 输出无法解析统计行: " + truncateLines(text, 5)}
	}
	out.Transmitted, out.Received, out.LossPct = tx, rx, loss
	out.MinMs, out.AvgMs, out.MaxMs = minMs, avgMs, maxMs
	return nil, out, nil
}

var (
	pingStatsRe = regexp.MustCompile(`(\d+) packets transmitted, (\d+)(?: packets)? received, ([\d.]+)% packet loss`)
	pingRTTRe   = regexp.MustCompile(`(?:round-trip|rtt) min/avg/max(?:/(?:mdev|stddev))? = ([\d.]+)/([\d.]+)/([\d.]+)(?:/[\d.]+)? ?ms`)
)

// parsePingOutput 解析 ping 统计摘要（兼容 busybox 与 iputils 两种措辞）。
func parsePingOutput(text string) (tx, rx int, lossPct, minMs, avgMs, maxMs float64, ok bool) {
	if m := pingStatsRe.FindStringSubmatch(text); m != nil {
		tx, _ = strconv.Atoi(m[1])
		rx, _ = strconv.Atoi(m[2])
		lossPct, _ = strconv.ParseFloat(m[3], 64)
	} else {
		return 0, 0, 0, 0, 0, 0, false
	}
	if m := pingRTTRe.FindStringSubmatch(text); m != nil {
		minMs, _ = strconv.ParseFloat(m[1], 64)
		avgMs, _ = strconv.ParseFloat(m[2], 64)
		maxMs, _ = strconv.ParseFloat(m[3], 64)
	}
	return tx, rx, lossPct, minMs, avgMs, maxMs, true
}

// RegisterDiagnostics 注册 diagnostics 域工具。
func RegisterDiagnostics(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.public_ip",
		Description: "Compare WAN IP (ubus) with egress public IP (curl ipify), detect NAT/CGNAT (10/8, 172.16/12, 192.168/16, 100.64/10). Degrades to WAN IP only when curl is unavailable.",
	}, publicIPHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.dns_resolve",
		Description: "Resolve a domain via nslookup (optional custom DNS server), returning the answering server and address list.",
	}, dnsResolveHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.dns_config",
		Description: "Show DNS configuration: resolv.conf nameservers plus dnsmasq upstream server=/address= entries and noresolv flag from uci dhcp.",
	}, dnsConfigHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.routes",
		Description: "Routing tables: full IPv4 (ip route, truncated) plus first 20 lines of IPv6 routes when available.",
	}, routesHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.ping",
		Description: "Ping a host (default 4, max 10 packets) and report transmitted/received, packet loss and RTT min/avg/max.",
	}, pingHandler)
}
