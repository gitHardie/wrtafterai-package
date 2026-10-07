// configview 域：router.get_config / router.wireless_config —— uci 配置只读视图，输出前敏感键脱敏。
package tools

import (
	"context"
	"regexp"
	"strings"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sensitiveKeySubstrings 键名含这些子串（不区分大小写）即视为敏感。
var sensitiveKeySubstrings = []string{"password", "key", "psk", "secret", "token"}

// isSensitiveKey 判断 uci 键名（如 wireless.@wifi-iface[0].key）是否敏感。
func isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}

// maskSensitiveLines 对 "uci show" 文本逐行脱敏：敏感键的值替换为 '***'。
// 返回脱敏后的文本与脱敏条数。
func maskSensitiveLines(text string) (string, int) {
	lines := strings.Split(text, "\n")
	count := 0
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		k, _, ok := strings.Cut(line, "=")
		if !ok || !isSensitiveKey(k) {
			continue
		}
		lines[i] = k + "=***"
		count++
	}
	return strings.Join(lines, "\n"), count
}

// pkgRe / secRe get_config 入参白名单（section 允许 @zone[0] 形式）。
var (
	pkgRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	secRe = regexp.MustCompile(`^[@a-zA-Z0-9_\[\]-]+$`)
)

// ---- router.get_config ----

type getConfigOutput struct {
	Package     string `json:"package"`
	Section     string `json:"section,omitempty"`
	MaskedCount int    `json:"masked_count,omitempty"`
	Config      string `json:"config"`
}

type getConfigInput struct {
	Package string `json:"package" jsonschema:"uci package name (e.g. network, dhcp, wireless)"`
	Section string `json:"section,omitempty" jsonschema:"optional section name or @type[index] form"`
}

func getConfigHandler(ctx context.Context, req *mcp.CallToolRequest, in getConfigInput) (*mcp.CallToolResult, getConfigOutput, error) {
	var out getConfigOutput
	if !pkgRe.MatchString(in.Package) {
		return nil, out, &simpleError{"非法 package 名: 仅允许字母/数字/下划线/横线"}
	}
	if in.Section != "" && !secRe.MatchString(in.Section) {
		return nil, out, &simpleError{"非法 section 名: 仅允许字母/数字/下划线/横线/@/[]"}
	}

	target := in.Package
	out.Package = in.Package
	if in.Section != "" {
		target = in.Package + "." + in.Section
		out.Section = in.Section
	}

	raw, err := backend.UCIShow(ctx, target)
	if err != nil {
		return nil, out, &simpleError{"读取配置失败（package/section 可能不存在）: " + briefErr(err)}
	}
	masked, n := maskSensitiveLines(raw)
	out.MaskedCount = n
	out.Config = truncateLines(masked, 200)
	return nil, out, nil
}

// ---- router.wireless_config ----

type wirelessDeviceOut struct {
	Name     string `json:"name"`
	Channel  string `json:"channel,omitempty"`
	Band     string `json:"band,omitempty"`
	Htmode   string `json:"htmode,omitempty"`
	Txpower  string `json:"txpower,omitempty"`
	Disabled bool   `json:"disabled"`
}

type wirelessIfaceOut struct {
	Name       string `json:"name,omitempty"`
	Ssid       string `json:"ssid,omitempty"`
	Encryption string `json:"encryption,omitempty"`
	Device     string `json:"device,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Network    string `json:"network,omitempty"`
}

type wirelessConfigOutput struct {
	DeviceCount int                 `json:"device_count"`
	IfaceCount  int                 `json:"iface_count"`
	Devices     []wirelessDeviceOut `json:"devices,omitempty"`
	Ifaces      []wirelessIfaceOut  `json:"ifaces,omitempty"`
	Excerpt     string              `json:"excerpt,omitempty"`
	MaskedCount int                 `json:"masked_count,omitempty"`
	Note        string              `json:"note,omitempty"`
}

type wirelessConfigInput struct{}

func wirelessConfigHandler(ctx context.Context, req *mcp.CallToolRequest, in wirelessConfigInput) (*mcp.CallToolResult, wirelessConfigOutput, error) {
	var out wirelessConfigOutput

	raw, err := backend.UCIShow(ctx, "wireless")
	if err != nil {
		return nil, out, &simpleError{"读取 wireless 配置失败: " + briefErr(err)}
	}
	secs := parseUCISectionsOf(raw, "wireless")
	if len(secs) == 0 {
		out.Note = "无 wireless 配置（设备可能没有无线电或驱动未加载）"
		return nil, out, nil
	}

	for _, sec := range sortedKeys(secs) {
		m := secs[sec]
		switch m[secTypeKey] {
		case "wifi-device":
			out.Devices = append(out.Devices, wirelessDeviceOut{
				Name:     sec,
				Channel:  m["channel"],
				Band:     m["band"],
				Htmode:   m["htmode"],
				Txpower:  m["txpower"],
				Disabled: m["disabled"] == "1" || m["disabled"] == "true",
			})
			out.DeviceCount++
		case "wifi-iface":
			out.Ifaces = append(out.Ifaces, wirelessIfaceOut{
				Name:       sec,
				Ssid:       m["ssid"],
				Encryption: m["encryption"],
				Device:     m["device"],
				Mode:       m["mode"],
				Network:    m["network"],
			})
			out.IfaceCount++
		}
	}

	masked, n := maskSensitiveLines(raw)
	out.MaskedCount = n
	out.Excerpt = truncateLines(masked, 40)
	return nil, out, nil
}

// ---- 通用 uci 解析 helpers（logs.go 的 parseUCISections 专用于 firewall，勿复用） ----

// secTypeKey 是段定义行（如 wireless.radio0=wifi-device）存入键值 map 的标记键。
// 不能用 "type"：wireless 段自带 type='mac80211' 选项会冲突。
const secTypeKey = "__section_type__"

// parseUCISectionsOf 解析 "uci show <pkg>" 文本为 段名→键值 map（通用包名版本）。
// 段定义行写入 secTypeKey；键值行去掉包名前缀与引号；非本包行跳过。
func parseUCISectionsOf(text, pkg string) map[string]map[string]string {
	out := map[string]map[string]string{}
	prefix := pkg + "."
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(k, prefix) {
			continue
		}
		k = strings.TrimPrefix(k, prefix)
		v = strings.Trim(v, `'"`)
		sec, opt, hasOpt := strings.Cut(k, ".")
		m := out[sec]
		if m == nil {
			m = map[string]string{}
			out[sec] = m
		}
		if !hasOpt {
			m[secTypeKey] = v
		} else {
			m[opt] = v
		}
	}
	return out
}

// splitUCIValues 拆分 uci list 值：`'a' 'b'` → [a, b]；单值去引号；空值返回 nil。
func splitUCIValues(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.Fields(v) {
		if t := strings.Trim(tok, `'"`); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// RegisterConfigView 注册 configview 域工具。
func RegisterConfigView(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.get_config",
		Description: "Read a uci package (or single section) with sensitive keys (password/key/psk/secret/token) masked as '***'. Output truncated to 200 lines.",
	}, getConfigHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.wireless_config",
		Description: "Structured wireless configuration: wifi-device (channel/band/htmode/txpower/disabled) and wifi-iface (ssid/encryption/device/mode/network), sensitive fields masked.",
	}, wirelessConfigHandler)
}
