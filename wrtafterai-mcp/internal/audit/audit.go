// Package audit：JSONL 审计日志 + 最近事件环形缓存。
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry 一条审计记录。
type Entry struct {
	TS        string `json:"ts"`
	Token     string `json:"token"`
	Tool      string `json:"tool"`
	Level     string `json:"level"`
	Allowed   bool   `json:"allowed"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// Logger JSONL 追加写入，线程安全；Recent 提供最近 n 条（doctor 用）。
type Logger struct {
	mu     sync.Mutex
	f      *os.File
	recent []Entry
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f}, nil
}

func (l *Logger) Log(e Entry) {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(e)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.f.Write(append(b, '\n'))
	l.recent = append(l.recent, e)
	if len(l.recent) > 200 {
		l.recent = l.recent[len(l.recent)-200:]
	}
}

func (l *Logger) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.recent) {
		n = len(l.recent)
	}
	out := make([]Entry, n)
	copy(out, l.recent[len(l.recent)-n:])
	return out
}

func (l *Logger) Close() error { return l.f.Close() }
