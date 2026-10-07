package policy

import (
	"testing"
	"time"
)

func testPolicy() *Policy {
	return New([]Token{
		{Name: "ro", Key: "key-ro", Level: LevelReadonly},
		{Name: "rw", Key: "key-rw", Level: LevelWrite},
	})
}

func TestAuthSuccess(t *testing.T) {
	p := testPolicy()
	tok, ok, locked := p.Auth("1.2.3.4", "key-ro")
	if !ok || locked || tok == nil {
		t.Fatalf("expected success, ok=%v locked=%v tok=%v", ok, locked, tok)
	}
	if tok.Name != "ro" || tok.Level != LevelReadonly {
		t.Fatalf("wrong token: %+v", tok)
	}
}

func TestAuthWrongKeyRejected(t *testing.T) {
	p := testPolicy()
	tok, ok, locked := p.Auth("1.2.3.4", "wrong-key")
	if ok || locked || tok != nil {
		t.Fatalf("wrong key must be rejected: ok=%v locked=%v tok=%v", ok, locked, tok)
	}
	// 空 key 同样拒绝
	if _, ok, _ := p.Auth("1.2.3.4", ""); ok {
		t.Fatal("empty key must be rejected")
	}
}

func TestLockoutAfterFiveFails(t *testing.T) {
	p := testPolicy()
	for i := 1; i <= 5; i++ {
		_, ok, locked := p.Auth("9.9.9.9", "bad")
		if locked {
			t.Fatalf("fail #%d: locked too early", i)
		}
		if ok {
			t.Fatalf("fail #%d: bad key accepted", i)
		}
	}
	// 第 6 次即使 key 正确也应处于锁定期
	_, ok, locked := p.Auth("9.9.9.9", "key-ro")
	if ok || !locked {
		t.Fatalf("after 5 failures expect locked, got ok=%v locked=%v", ok, locked)
	}
	// 其他 IP 不受影响
	if _, ok, locked := p.Auth("8.8.8.8", "key-ro"); !ok || locked {
		t.Fatal("other ip must not be locked")
	}
}

func TestUnlockRestoresAuth(t *testing.T) {
	p := testPolicy()
	for i := 0; i < 5; i++ {
		p.Auth("9.9.9.9", "bad")
	}
	if _, _, locked := p.Auth("9.9.9.9", "key-ro"); !locked {
		t.Fatal("precondition: should be locked")
	}
	// 模拟锁定期结束（同包测试直接改内部状态）
	p.mu.Lock()
	p.fails["9.9.9.9"].until = time.Now().Add(-time.Minute)
	p.mu.Unlock()

	tok, ok, locked := p.Auth("9.9.9.9", "key-ro")
	if !ok || locked || tok == nil {
		t.Fatalf("after unlock expect success, got ok=%v locked=%v tok=%v", ok, locked, tok)
	}
}

func TestAllowedLevels(t *testing.T) {
	p := testPolicy()
	ro, ok, _ := p.Auth("1.1.1.1", "key-ro")
	if !ok {
		t.Fatal("precondition ro auth")
	}
	rw, ok, _ := p.Auth("1.1.1.1", "key-rw")
	if !ok {
		t.Fatal("precondition rw auth")
	}
	if !p.Allowed(ro, LevelReadonly) {
		t.Fatal("readonly token must access readonly tools")
	}
	if p.Allowed(ro, LevelWrite) {
		t.Fatal("readonly token must NOT access write tools")
	}
	if !p.Allowed(rw, LevelWrite) || !p.Allowed(rw, LevelReadonly) {
		t.Fatal("write token must access both levels")
	}
}

func TestParseLevel(t *testing.T) {
	if ParseLevel("write") != LevelWrite {
		t.Fatal("write should parse to write")
	}
	if ParseLevel("readonly") != LevelReadonly {
		t.Fatal("readonly should parse to readonly")
	}
	if ParseLevel("garbage") != LevelReadonly {
		t.Fatal("unknown level must default to readonly")
	}
}
