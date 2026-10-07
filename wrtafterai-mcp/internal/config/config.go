// Package config 读取 uci 配置 wrtafterai-mcp；非路由器环境回退默认值+环境变量（便于本地开发）。
package config

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gitHardie/wrtafterai-mcp/internal/policy"
)

// Config 顶层配置。
type Config struct {
	Listen string // 监听地址，默认 0.0.0.0:8443
	Domain string // 局域网访问域名，默认 wrt.afterai
	TLS    bool   // 是否启用自签 TLS
	Tokens []policy.Token
	Audit  string // 审计日志路径
}

// Default 返回默认配置（uci 不可用时兜底）。
func Default() *Config {
	return &Config{
		Listen: envOr("WRT_MCP_LISTEN", "0.0.0.0:8443"),
		Domain: envOr("WRT_MCP_DOMAIN", "wrt.afterai"),
		TLS:    envBool("WRT_MCP_TLS", false),
		Audit:  envOr("WRT_MCP_AUDIT", "/tmp/wrtafterai-mcp/audit.jsonl"),
	}
}

// Load 依次尝试 uci → 默认+环境变量。
func Load() (*Config, error) {
	cfg := Default()
	uc, err := loadUCI()
	if err != nil {
		return cfg, nil // 非 OpenWrt 环境：静默回退
	}
	if v := uc.get("server.listen"); v != "" {
		cfg.Listen = v
	}
	if v := uc.get("server.domain"); v != "" {
		cfg.Domain = v
	}
	if v := uc.get("server.tls"); v != "" {
		cfg.TLS = v == "1" || v == "true"
	}
	if v := uc.get("server.audit"); v != "" {
		cfg.Audit = v
	}
	for name, tk := range uc.tokens {
		if tk.key == "" {
			continue
		}
		lvl := policy.ParseLevel(tk.level)
		cfg.Tokens = append(cfg.Tokens, policy.Token{Name: name, Key: tk.key, Level: lvl})
	}
	return cfg, nil
}

// ---- uci 解析 ----

type uciData struct {
	simple map[string]string // server.listen -> v
	tokens map[string]*struct{ key, level string }
}

func (u *uciData) get(k string) string { return u.simple[k] }

// uciReader 是可替换的 uci 读取入口（单测注入假输出用）。
var uciReader = func() ([]byte, error) {
	return exec.Command("uci", "-q", "show", "wrtafterai-mcp").Output()
}

func loadUCI() (*uciData, error) {
	out, err := uciReader()
	if err != nil {
		return nil, err
	}
	u := &uciData{simple: map[string]string{}, tokens: map[string]*struct{ key, level string }{}}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, "'\"")
		k = strings.TrimPrefix(k, "wrtafterai-mcp.")
		parts := strings.Split(k, ".")
		if len(parts) < 2 {
			continue
		}
		sec, opt := parts[0], parts[1]
		if sec == "server" {
			u.simple[sec+"."+opt] = v // 存完整 key，与 Load 的 get("server.listen") 对齐
			continue
		}
		// token 段：server 之外的命名段均为 token
		t := u.tokens[sec]
		if t == nil {
			t = &struct{ key, level string }{}
			u.tokens[sec] = t
		}
		switch opt {
		case "key":
			t.key = v
		case "level":
			t.level = v
		}
	}
	return u, nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envBool(k string, d bool) bool {
	if v := os.Getenv(k); v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	return d
}
