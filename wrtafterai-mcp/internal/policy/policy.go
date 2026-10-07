// Package policy：token 校验、权限分级、失败限速。
package policy

import (
	"crypto/subtle"
	"sync"
	"time"
)

// Level 权限级别。readonly：仅只读工具；write：只读 + 受控写（confirm 流程，M1）。
type Level string

const (
	LevelReadonly Level = "readonly"
	LevelWrite    Level = "write"
)

// Token 一个 AI 客户端的接入凭证。
type Token struct {
	Name  string
	Key   string
	Level Level
}

func ParseLevel(s string) Level {
	if Level(s) == LevelWrite {
		return LevelWrite
	}
	return LevelReadonly
}

// Policy 持有全部 token，提供认证与限速。
type Policy struct {
	mu     sync.RWMutex
	tokens []Token
	fails  map[string]*failWindow
}

type failWindow struct {
	count int
	until time.Time
}

func New(tokens []Token) *Policy {
	return &Policy{tokens: tokens, fails: map[string]*failWindow{}}
}

// Auth 按 key 查 token（constant-time）。locked=true 表示来源 IP 处于限速锁定期。
func (p *Policy) Auth(ip, key string) (*Token, bool, bool) {
	if p.isLocked(ip) {
		return nil, false, true
	}
	for i := range p.tokens {
		t := &p.tokens[i]
		if subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) == 1 {
			return t, true, false
		}
	}
	p.recordFail(ip)
	return nil, false, false
}

// Allowed 判断 token 是否可以使用某级别的工具。
func (p *Policy) Allowed(t *Token, need Level) bool {
	if need == LevelReadonly {
		return true // 一切 token 至少可读
	}
	return t.Level == LevelWrite
}

func (p *Policy) isLocked(ip string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	f, ok := p.fails[ip]
	return ok && time.Now().Before(f.until)
}

func (p *Policy) recordFail(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.fails[ip]
	if f == nil {
		f = &failWindow{}
		p.fails[ip] = f
	}
	f.count++
	if f.count >= 5 {
		f.until = time.Now().Add(15 * time.Minute)
		f.count = 0
	}
}
