package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
)

func TestParseServiceListJSON(t *testing.T) {
	data := []byte(`{
		"dnsmasq": {"instances": {"instance1": {"running": true}}},
		"firewall": {"instances": {"instance1": {"running": true}}},
		"odhcpd": {},
		"dropbear": {"instances": {}}
	}`)
	got := parseServiceListJSON(data)
	if len(got) != 2 {
		t.Fatalf("got %v, want only dnsmasq+firewall running", got)
	}
	if !got["dnsmasq"] || !got["firewall"] {
		t.Fatalf("running set wrong: %v", got)
	}
	if got["odhcpd"] || got["dropbear"] {
		t.Fatalf("empty instances must not be running: %v", got)
	}
}

func TestParseServiceListJSONGarbage(t *testing.T) {
	if got := parseServiceListJSON([]byte("not json")); len(got) != 0 {
		t.Fatalf("garbage should give empty map, got %v", got)
	}
}

func TestScanInitdServices(t *testing.T) {
	dir := t.TempDir()
	initd := filepath.Join(dir, "init.d")
	rcd := filepath.Join(dir, "rc.d")
	os.MkdirAll(initd, 0o755)
	os.MkdirAll(rcd, 0o755)
	for _, n := range []string{"dnsmasq", "firewall", "dockerd", "README"} {
		os.WriteFile(filepath.Join(initd, n), []byte("#!/bin/sh\n"), 0o755)
	}
	for _, n := range []string{"S19dnsmasq", "S50dockerd", "K10firewall"} {
		os.Symlink("/etc/init.d/x", filepath.Join(rcd, n))
	}

	names, enabled, err := scanInitdServices(initd, rcd)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 4 || names[0] != "README" || names[1] != "dnsmasq" {
		t.Fatalf("names wrong: %v", names)
	}
	if !enabled["dnsmasq"] || !enabled["dockerd"] {
		t.Errorf("enabled wrong: %v", enabled)
	}
	if enabled["firewall"] { // 只有 K 链接 = 未启用
		t.Errorf("firewall should not be enabled: %v", enabled)
	}
	if enabled["README"] {
		t.Error("README should not be enabled")
	}
}

func TestScanInitdServicesMissingDirs(t *testing.T) {
	if _, _, err := scanInitdServices(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "nope2")); err == nil {
		t.Fatal("missing init.d dir must error")
	}
	// rc.d 缺失 → 全部未启用，但不报错
	initd := filepath.Join(t.TempDir(), "init.d")
	os.MkdirAll(initd, 0o755)
	os.WriteFile(filepath.Join(initd, "dnsmasq"), []byte("#!/bin/sh\n"), 0o755)
	names, enabled, err := scanInitdServices(initd, filepath.Join(t.TempDir(), "no-rcd"))
	if err != nil {
		t.Fatalf("missing rc.d should not error: %v", err)
	}
	if len(names) != 1 || len(enabled) != 0 {
		t.Fatalf("names=%v enabled=%v", names, enabled)
	}
}

func TestDockerPSRows(t *testing.T) {
	text := "web\trunning\tUp 2 hours\tnginx:latest\r\n" +
		"db\texited\tExited (0) 3 days ago\tmariadb:10.5\n" +
		"\n" +
		"bad-line-with-fewer-columns\tonly\n"
	rows := parseDockerPS(text)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].Name != "web" || rows[0].State != "running" || rows[0].Status != "Up 2 hours" || rows[0].Image != "nginx:latest" {
		t.Errorf("row0 wrong: %+v", rows[0])
	}
	if rows[1].Name != "db" || rows[1].State != "exited" {
		t.Errorf("row1 wrong: %+v", rows[1])
	}
}

func TestDockerPSRowsEmpty(t *testing.T) {
	if rows := parseDockerPS(""); len(rows) != 0 {
		t.Fatalf("empty wrong: %v", rows)
	}
}

func TestDockerContainersHandlerNotInstalled(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()

	_, res, err := dockerContainersHandler(context.Background(), nil, dockerContainersInput{})
	if err != nil {
		t.Fatalf("not installed should degrade gracefully: %v", err)
	}
	if res.Installed || res.Note == "" {
		t.Fatalf("expected Installed=false with hint note: %+v", res)
	}
	if !strings.Contains(res.Note, "docker") {
		t.Errorf("note should mention docker: %q", res.Note)
	}
}

func TestDockerContainersHandlerOK(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	defer func() { lookPath = old }()

	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "docker" {
			return nil, errors.New("unexpected: " + name)
		}
		joined := strings.Join(args, " ")
		switch {
		case !contains(args, "-a") && joined == "ps --format "+dockerPSFormat:
			return []byte("web\trunning\tUp 2 hours\tnginx:latest\n"), nil
		case contains(args, "-a"):
			return []byte("web\trunning\tUp 2 hours\tnginx:latest\ndb\texited\tExited (0)\tmariadb:10.5\n"), nil
		}
		return nil, errors.New("unexpected args: " + joined)
	})
	defer restore()

	_, res, err := dockerContainersHandler(context.Background(), nil, dockerContainersInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed || res.All || res.Count != 1 {
		t.Fatalf("running-only wrong: %+v", res)
	}

	_, res, err = dockerContainersHandler(context.Background(), nil, dockerContainersInput{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 || !res.All {
		t.Fatalf("all wrong: %+v", res)
	}
}

func TestDockerContainersHandlerDaemonDown(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	defer func() { lookPath = old }()
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")
	})
	defer restore()
	if _, _, err := dockerContainersHandler(context.Background(), nil, dockerContainersInput{}); err == nil {
		t.Fatal("daemon down must return error")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
