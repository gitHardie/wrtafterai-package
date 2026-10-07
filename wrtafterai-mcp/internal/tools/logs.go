// logs 域：router.firewall_rules（uci firewall 摘要）+ router.system_log（logread）。
package tools

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- router.firewall_rules ----

type fwZoneOut struct {
	Name    string `json:"name,omitempty"`
	Forward string `json:"forward,omitempty"`
	Input   string `json:"input,omitempty"`
	Output  string `json:"output,omitempty"`
}

type fwRuleOut struct {
	Name    string `json:"name,omitempty"`
	Src     string `json:"src,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Target  string `json:"target,omitempty"`
	Proto   string `json:"proto,omitempty"`
	Enabled bool   `json:"enabled"` // uci 未写 enabled 默认为开
}

type firewallRulesOutput struct {
	ZoneCount     int         `json:"zone_count"`
	RuleCount     int         `json:"rule_count"`
	RedirectCount int         `json:"redirect_count,omitempty"`
	ForwardCount  int         `json:"forwarding_count,omitempty"`
	Zones         []fwZoneOut `json:"zones,omitempty"`
	Rules         []fwRuleOut `json:"rules,omitempty"`
	UCIExcerpt    string      `json:"uci_excerpt,omitempty"` // 截断后的原文摘录
}

type firewallRulesInput struct{}

func firewallRulesHandler(ctx context.Context, req *mcp.CallToolRequest, in firewallRulesInput) (*mcp.CallToolResult, firewallRulesOutput, error) {
	var out firewallRulesOutput

	raw, err := backend.UCIShow(ctx, "firewall")
	if err != nil {
		return nil, out, &simpleError{"读取 firewall 配置失败: " + briefErr(err)}
	}
	secs := parseUCISections(raw)

	// 稳定输出顺序：uci 段名排序（@zone[0] < @zone[1] < @rule[0]...）
	for _, sec := range sortedKeys(secs) {
		m := secs[sec]
		switch m["type"] {
		case "zone":
			out.Zones = append(out.Zones, fwZoneOut{
				Name:    m["name"],
				Forward: m["forward"],
				Input:   m["input"],
				Output:  m["output"],
			})
			out.ZoneCount++
		case "rule":
			out.Rules = append(out.Rules, fwRuleOut{
				Name:    m["name"],
				Src:     m["src"],
				Dest:    m["dest"],
				Target:  m["target"],
				Proto:   m["proto"],
				Enabled: m["enabled"] != "0" && m["enabled"] != "false",
			})
			out.RuleCount++
		case "redirect":
			out.RedirectCount++
		case "forwarding":
			out.ForwardCount++
		}
	}
	out.UCIExcerpt = truncateLines(raw, 50)
	return nil, out, nil
}

// parseUCISections 把 "uci show firewall" 文本解析为 段名→键值 map。
// 段定义行（firewall.@zone[0]=zone）写入 type；键值行去掉包名前缀与引号。
func parseUCISections(text string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimPrefix(k, "firewall.")
		v = strings.Trim(v, `'"`)
		sec, opt, hasOpt := strings.Cut(k, ".")
		if !hasOpt {
			// 段定义行：firewall.@zone[0]=zone
			m := out[sec]
			if m == nil {
				m = map[string]string{}
				out[sec] = m
			}
			m["type"] = v
			continue
		}
		m := out[sec]
		if m == nil {
			m = map[string]string{}
			out[sec] = m
		}
		m[opt] = v
	}
	return out
}

// sortedKeys 按段名稳定排序（@rule[10] 与 @rule[2] 按字典序，仅保证输出稳定）。
func sortedKeys(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// ---- router.system_log ----

type systemLogOutput struct {
	Lines int    `json:"lines"`
	Log   string `json:"log"`
}

type systemLogInput struct {
	Lines int `json:"lines,omitempty" jsonschema:"number of recent log lines to return (10-200, default 80)"`
}

func systemLogHandler(ctx context.Context, req *mcp.CallToolRequest, in systemLogInput) (*mcp.CallToolResult, systemLogOutput, error) {
	var out systemLogOutput
	n := in.Lines
	if n <= 0 {
		n = 80
	}
	if n > 200 {
		n = 200
	}

	txt, err := backend.Run(ctx, 10*time.Second, "logread", "-l", strconv.Itoa(n))
	if err != nil {
		// 旧版 logread 无 -l 选项：全量读取后本地截尾
		full, err2 := backend.Run(ctx, 15*time.Second, "logread")
		if err2 != nil {
			return nil, out, &simpleError{"logread 不可用: " + briefErr(err2) + " (首次尝试: " + briefErr(err) + ")"}
		}
		txt = full
	}
	log := truncateLines(txt, n)
	out.Log = log
	out.Lines = len(strings.Split(strings.TrimSpace(log), "\n"))
	if log == "" {
		out.Lines = 0
	}
	return nil, out, nil
}

// ---- router.kernel_log ----

type kernelLogOutput struct {
	Lines int    `json:"lines"`
	Log   string `json:"log"`
}

type kernelLogInput struct {
	Lines int `json:"lines,omitempty" jsonschema:"number of recent kernel log lines (10-200, default 80)"`
}

func kernelLogHandler(ctx context.Context, req *mcp.CallToolRequest, in kernelLogInput) (*mcp.CallToolResult, kernelLogOutput, error) {
	var out kernelLogOutput
	n := in.Lines
	if n <= 0 {
		n = 80
	}
	if n > 200 {
		n = 200
	}

	txt, err := backend.Run(ctx, 10*time.Second, "dmesg")
	if err != nil {
		return nil, out, &simpleError{"dmesg 不可用: " + briefErr(err)}
	}
	log := truncateLines(txt, n)
	out.Log = log
	out.Lines = len(strings.Split(strings.TrimSpace(log), "\n"))
	if log == "" {
		out.Lines = 0
	}
	return nil, out, nil
}

// RegisterLogs 注册 logs 域工具。
func RegisterLogs(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.firewall_rules",
		Description: "Firewall summary from uci: zones (name/forward/input/output), rules (name/src/dest/target/enabled), redirects and forwardings. Returns summary plus a truncated uci excerpt, never the full dump.",
	}, firewallRulesHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.system_log",
		Description: "Recent system log via logread (default last 80 lines, max 200). Use for diagnosing service failures, dhcp/firewall errors.",
	}, systemLogHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.kernel_log",
		Description: "Recent kernel log via dmesg (default last 80 lines, max 200). Use for hardware/driver/network issues that never reach syslog.",
	}, kernelLogHandler)
}
