package config

import (
	"errors"
	"testing"

	"github.com/gitHardie/wrtafterai-mcp/internal/policy"
)

const fakeUCI = `wrtafterai-mcp.server=server
wrtafterai-mcp.server.listen='0.0.0.0:9999'
wrtafterai-mcp.server.domain='wrt.afterai'
wrtafterai-mcp.server.tls='1'
wrtafterai-mcp.server.audit='/tmp/audit-test.jsonl'
wrtafterai-mcp.claude=token
wrtafterai-mcp.claude.key='sk-claude-1'
wrtafterai-mcp.claude.level='write'
wrtafterai-mcp.gpt=token
wrtafterai-mcp.gpt.key='sk-gpt-1'
wrtafterai-mcp.gpt.level='readonly'
`

func withFakeUCI(t *testing.T, out []byte, err error) {
	t.Helper()
	old := uciReader
	uciReader = func() ([]byte, error) { return out, err }
	t.Cleanup(func() { uciReader = old })
}

func TestLoadUCIServerAndTokens(t *testing.T) {
	withFakeUCI(t, []byte(fakeUCI), nil)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9999" {
		t.Errorf("Listen = %q, want 0.0.0.0:9999", cfg.Listen)
	}
	if cfg.Domain != "wrt.afterai" {
		t.Errorf("Domain = %q", cfg.Domain)
	}
	if !cfg.TLS {
		t.Error("TLS = false, want true")
	}
	if cfg.Audit != "/tmp/audit-test.jsonl" {
		t.Errorf("Audit = %q", cfg.Audit)
	}
	if len(cfg.Tokens) != 2 {
		t.Fatalf("got %d tokens, want 2: %+v", len(cfg.Tokens), cfg.Tokens)
	}
	byName := map[string]policy.Token{}
	for _, tk := range cfg.Tokens {
		byName[tk.Name] = tk
	}
	c, ok := byName["claude"]
	if !ok || c.Key != "sk-claude-1" || c.Level != policy.LevelWrite {
		t.Errorf("claude token wrong: %+v ok=%v", c, ok)
	}
	g, ok := byName["gpt"]
	if !ok || g.Key != "sk-gpt-1" || g.Level != policy.LevelReadonly {
		t.Errorf("gpt token wrong: %+v ok=%v", g, ok)
	}
}

func TestLoadUCIErrorFallsBackToDefaults(t *testing.T) {
	withFakeUCI(t, nil, errors.New("uci: not found"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must silently fall back, got error: %v", err)
	}
	if cfg.Listen == "" || cfg.Domain == "" || cfg.Audit == "" {
		t.Fatalf("defaults missing: %+v", cfg)
	}
	if len(cfg.Tokens) != 0 {
		t.Fatalf("no tokens expected on uci failure: %+v", cfg.Tokens)
	}
}

func TestLoadUCIMissingKeySkipped(t *testing.T) {
	withFakeUCI(t, []byte(`wrtafterai-mcp.broken=token
wrtafterai-mcp.broken.level='readonly'
wrtafterai-mcp.server.listen='1.2.3.4:1'
`), nil)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tokens) != 0 {
		t.Fatalf("token without key must be skipped: %+v", cfg.Tokens)
	}
	if cfg.Listen != "1.2.3.4:1" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
}

func TestLoadUCIUnknownLevelDefaultsReadonly(t *testing.T) {
	withFakeUCI(t, []byte(`wrtafterai-mcp.weird=token
wrtafterai-mcp.weird.key='k1'
wrtafterai-mcp.weird.level='admin'
`), nil)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tokens) != 1 || cfg.Tokens[0].Level != policy.LevelReadonly {
		t.Fatalf("unknown level must fall back to readonly: %+v", cfg.Tokens)
	}
}
