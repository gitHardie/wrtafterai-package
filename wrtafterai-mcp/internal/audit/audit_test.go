package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentReadback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	for i := 0; i < 5; i++ {
		l.Log(Entry{
			Token: "tester", Tool: "router.system_info",
			Level: "readonly", Allowed: true, LatencyMS: int64(i),
		})
	}

	got := l.Recent(3)
	if len(got) != 3 {
		t.Fatalf("Recent(3) = %d entries", len(got))
	}
	// 最后一条是最新写入
	if got[2].LatencyMS != 4 || got[0].LatencyMS != 2 {
		t.Fatalf("order wrong: first=%d last=%d, want 2/4", got[0].LatencyMS, got[2].LatencyMS)
	}
	if got[2].Token != "tester" || got[2].Tool != "router.system_info" {
		t.Fatalf("fields mismatch: %+v", got[2])
	}

	all := l.Recent(10)
	if len(all) != 5 {
		t.Fatalf("Recent(10) = %d entries, want 5", len(all))
	}
}

func TestRecentRingCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	for i := 0; i < 210; i++ {
		l.Log(Entry{Tool: "t", LatencyMS: int64(i)})
	}
	if got := l.Recent(1000); len(got) != 200 {
		t.Fatalf("ring cache cap: got %d, want 200", len(got))
	}
	if got := l.Recent(1); got[0].LatencyMS != 209 {
		t.Fatalf("newest entry lost: %+v", got[0])
	}
}

func TestJSONLPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Log(Entry{Token: "a", Tool: "router.dhcp_leases", Level: "readonly", Allowed: true})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expect 1 jsonl line, got %d", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("invalid jsonl: %v", err)
	}
	if e.Tool != "router.dhcp_leases" || e.Token != "a" || e.TS == "" {
		t.Fatalf("roundtrip mismatch: %+v", e)
	}
}
