// Package server：serve 模式（streamable HTTP + Bearer 认证 / stdio 本地模式）。
package server

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/audit"
	"github.com/gitHardie/wrtafterai-mcp/internal/config"
	"github.com/gitHardie/wrtafterai-mcp/internal/policy"
	"github.com/gitHardie/wrtafterai-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run 子命令入口。args 为 serve 之后的参数。
func Run(args []string, version string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	stdio := fs.Bool("stdio", false, "serve over stdio (local CLI agents)")
	dev := fs.Bool("dev", false, "dev mode: allow env-token WRT_MCP_DEV_TOKEN=level")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *dev {
		if v := devToken(); v != "" {
			name, key, _ := strings.Cut(v, "=")
			cfg.Tokens = append(cfg.Tokens, policy.Token{Name: name, Key: key, Level: policy.ParseLevel(strings.SplitN(key, ":", 2)[1])})
		}
	}

	auditLog, err := audit.New(cfg.Audit)
	if err != nil {
		log.Printf("audit disabled: %v", err)
	}
	pol := policy.New(cfg.Tokens)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "wrtafterai-mcp",
		Version: version,
	}, nil)

	deps := &tools.Deps{Audit: auditLog}
	tools.RegisterSystem(server)
	// 其余域由 tools.RegisterAll 收口（见 tools/registry.go 的 RegisterAll 实现）
	tools.RegisterAll(server, deps)

	if *stdio {
		return server.Run(context.Background(), &mcp.StdioTransport{})
	}

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.Handle("/mcp", authWrap(pol, auditLog, h))

	log.Printf("wrtafterai-mcp %s listening on http://%s/mcp (LAN domain: http://%s:%s/mcp)",
		version, cfg.Listen, cfg.Domain, portOf(cfg.Listen))
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// authWrap Bearer token 校验 + 审计（工具级审计在 middleware 中按 JSON-RPC method 记录）。
func authWrap(pol *policy.Policy, a *audit.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		if key == "" {
			json401(w, "missing bearer token; get one on the router: wrtafterai-mctl token add")
			return
		}
		ip := strings.SplitN(r.RemoteAddr, ":", 2)[0]
		t, ok, locked := pol.Auth(ip, key)
		if locked {
			json401(w, "too many failures, locked 15min")
			return
		}
		if !ok {
			if a != nil {
				a.Log(audit.Entry{Token: "unknown", Tool: "AUTH", Level: "-", Allowed: false, Error: "bad token from " + ip})
			}
			json401(w, "invalid token")
			return
		}
		// token 身份透传给审计 middleware
		r = r.WithContext(context.WithValue(r.Context(), tokenKey{}, t))
		next.ServeHTTP(w, r)
	})
}

type tokenKey struct{}

func devToken() string { return "" } // 占位：dev 模式由环境变量直接注入（见 Run）

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func json401(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func portOf(listen string) string {
	if _, p, ok := strings.Cut(listen, ":"); ok {
		return p
	}
	return "8443"
}

