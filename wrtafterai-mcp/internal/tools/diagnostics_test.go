package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
)

func TestValidHost(t *testing.T) {
	valid := []string{
		"example.com", "a.b.c.example.co.uk", "router", "192.168.1.1",
		"8.8.8.8", "my-host.example", "sub_domain.example.com", "xn--fiq228c.com",
		"a" + strings.Repeat(".b", 120), // < 253
	}
	for _, s := range valid {
		if !validHost(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	invalid := []string{
		"", "-abc", ".example.com", "a b", "a;b", "a|b", "例子.com",
		"a" + strings.Repeat(".b", 130), // > 253
		"a'b", "a`b", "a$b", "$(x)", "1.2.3.4;rm -rf /",
	}
	for _, s := range invalid {
		if validHost(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestIsPrivateOrCGNAT(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
		cgnat   bool
	}{
		{"10.0.0.1", true, false},
		{"10.255.255.255", true, false},
		{"172.16.0.1", true, false},
		{"172.31.255.255", true, false},
		{"192.168.1.1", true, false},
		{"100.64.0.1", false, true},
		{"100.127.255.254", false, true},
		{"8.8.8.8", false, false},
		{"172.32.0.1", false, false}, // 172.16/12 之外
		{"172.15.255.255", false, false},
		{"100.63.255.255", false, false}, // CGNAT 段之外
		{"100.128.0.1", false, false},
		{"::1", false, false}, // IPv6 不判
		{"not-an-ip", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		if got := isPrivateIPv4(c.ip); got != c.private {
			t.Errorf("isPrivateIPv4(%q) = %v, want %v", c.ip, got, c.private)
		}
		if got := isCGNAT(c.ip); got != c.cgnat {
			t.Errorf("isCGNAT(%q) = %v, want %v", c.ip, got, c.cgnat)
		}
		if got := isPrivateOrCGNAT(c.ip); got != (c.private || c.cgnat) {
			t.Errorf("isPrivateOrCGNAT(%q) = %v", c.ip, got)
		}
	}
}

func TestClassifyPublicIP(t *testing.T) {
	cases := []struct {
		name      string
		wan, pub  string
		note      string
		wantNAT   bool
		wantType  string
		wantMatch bool
		wantNote  string
	}{
		{
			name: "公网直连一致",
			wan:  "203.0.113.5", pub: "203.0.113.5",
			wantMatch: true,
		},
		{
			name: "公网直连一致且自带note",
			wan:  "203.0.113.5", pub: "203.0.113.5", note: "x",
			wantMatch: true, wantNote: "x",
		},
		{
			name: "私网NAT后",
			wan:  "192.168.1.2", pub: "203.0.113.9",
			wantNAT: true, wantType: "私网 NAT",
		},
		{
			name: "CGNAT后",
			wan:  "100.64.10.10", pub: "203.0.113.9",
			wantNAT: true, wantType: "CGNAT (100.64/10)",
		},
		{
			name: "出口与公网WAN不一致",
			wan:  "203.0.113.5", pub: "198.51.100.1",
			wantNote: "出口 IP 与 WAN IP 不一致（可能存在多 WAN 或上游代理）",
		},
		{
			name: "curl失败仅有WAN",
			wan:  "203.0.113.5", note: "curl 不可用，仅返回 WAN IP",
			wantNote: "curl 不可用，仅返回 WAN IP",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nat, typ, match, note := classifyPublicIP(c.wan, c.pub, c.note)
			if nat != c.wantNAT || typ != c.wantType || match != c.wantMatch {
				t.Fatalf("nat=%v type=%q match=%v, want nat=%v type=%q match=%v", nat, typ, match, c.wantNAT, c.wantType, c.wantMatch)
			}
			if note != c.wantNote {
				t.Fatalf("note = %q, want %q", note, c.wantNote)
			}
		})
	}
}

func TestParseNslookup(t *testing.T) {
	// busybox 新格式（Address N: ip host）
	text := `Server:		127.0.0.1
Address:	127.0.0.1:53

Name:      example.com
Address 1: 93.184.216.34 dns.example.com
Address 2: 2606:2800:220:1:248:1893:25c8:1946
Address 1: 93.184.216.34 dup.example.com
`
	server, answers := parseNslookup(text)
	if server != "127.0.0.1" {
		t.Errorf("server = %q", server)
	}
	if len(answers) != 2 || answers[0] != "93.184.216.34" || answers[1] != "2606:2800:220:1:248:1893:25c8:1946" {
		t.Errorf("answers wrong: %v", answers)
	}

	// 旧格式（Address: ip）
	old := `Server: 192.168.1.1
Address: 192.168.1.1

Name: example.com
Address: 1.2.3.4
`
	server, answers = parseNslookup(old)
	if server != "192.168.1.1" || len(answers) != 1 || answers[0] != "1.2.3.4" {
		t.Errorf("old format wrong: server=%q answers=%v", server, answers)
	}
}

func TestParseNslookupGarbage(t *testing.T) {
	if s, a := parseNslookup(""); s != "" || len(a) != 0 {
		t.Errorf("empty input wrong: %q %v", s, a)
	}
	if s, a := parseNslookup("random error text\nno addresses"); s != "" || len(a) != 0 {
		t.Errorf("garbage wrong: %q %v", s, a)
	}
	// Server 出现在 Name 之后不应再更新（answer 区）
	text := "Name: x.com\nServer: 1.1.1.1\nAddress: 2.2.2.2\n"
	_, answers := parseNslookup(text)
	if len(answers) != 1 || answers[0] != "2.2.2.2" {
		t.Errorf("answer after Name wrong: %v", answers)
	}
}

func TestDNSResolveHandlerOK(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "nslookup" {
			return nil, errors.New("unexpected: " + name)
		}
		if len(args) != 2 || args[0] != "example.com" || args[1] != "8.8.8.8" {
			t.Errorf("args wrong: %v", args)
		}
		return []byte("Server: 8.8.8.8\nAddress: 8.8.8.8\n\nName: example.com\nAddress 1: 93.184.216.34\n"), nil
	})
	defer restore()
	_, res, err := dnsResolveHandler(context.Background(), nil, dnsResolveInput{Domain: "example.com", Server: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 1 || res.Answers[0] != "93.184.216.34" || res.Server != "8.8.8.8" {
		t.Fatalf("out wrong: %+v", res)
	}
}

func TestDNSResolveHandlerInvalidDomain(t *testing.T) {
	for _, d := range []string{"", "-bad", "a b", "例子.com"} {
		if _, _, err := dnsResolveHandler(context.Background(), nil, dnsResolveInput{Domain: d}); err == nil {
			t.Errorf("domain %q must be rejected", d)
		}
	}
	if _, _, err := dnsResolveHandler(context.Background(), nil, dnsResolveInput{Domain: "ok.com", Server: "bad server"}); err == nil {
		t.Error("bad server must be rejected")
	}
}

func TestDNSResolveHandlerNXDOMAIN(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("nslookup: can't resolve 'nope.example': Name or service not known")
	})
	defer restore()
	if _, _, err := dnsResolveHandler(context.Background(), nil, dnsResolveInput{Domain: "nope.example"}); err == nil {
		t.Fatal("expected error for unresolvable domain")
	}
}

func TestParsePingOutput(t *testing.T) {
	busybox := `PING 1.1.1.1 (1.1.1.1): 56 data bytes
64 bytes from 1.1.1.1: seq=0 ttl=57 time=10.5 ms
64 bytes from 1.1.1.1: seq=1 ttl=57 time=11.5 ms

--- 1.1.1.1 ping statistics ---
2 packets transmitted, 2 packets received, 0% packet loss
round-trip min/avg/max = 10.5/11.0/11.5 ms
`
	tx, rx, loss, mn, avg, mx, ok := parsePingOutput(busybox)
	if !ok || tx != 2 || rx != 2 || loss != 0 || mn != 10.5 || avg != 11.0 || mx != 11.5 {
		t.Fatalf("busybox parse wrong: %d %d %v %v %v %v %v", tx, rx, loss, mn, avg, mx, ok)
	}

	// iputils 风格（丢包 + mdev）
	iputils := `3 packets transmitted, 1 received, 66% packet loss, time 2003ms
rtt min/avg/max/mdev = 10.100/10.200/10.300/0.100 ms
`
	tx, rx, loss, mn, avg, mx, ok = parsePingOutput(iputils)
	if !ok || tx != 3 || rx != 1 || loss != 66 || mn != 10.1 || avg != 10.2 || mx != 10.3 {
		t.Fatalf("iputils parse wrong: %d %d %v %v %v %v %v", tx, rx, loss, mn, avg, mx, ok)
	}

	if _, _, _, _, _, _, ok := parsePingOutput("no stats here"); ok {
		t.Fatal("garbage should not parse")
	}
}

func TestPingHandlerOK(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "ping" {
			return nil, errors.New("unexpected: " + name)
		}
		want := []string{"-c", "4", "-W", "2", "1.1.1.1"}
		if strings.Join(args, " ") != strings.Join(want, " ") {
			t.Errorf("args = %v, want %v", args, want)
		}
		return []byte("PING 1.1.1.1: 56 data bytes\n64 bytes from 1.1.1.1: seq=0 ttl=57 time=9.9 ms\n\n--- statistics ---\n4 packets transmitted, 4 packets received, 0% packet loss\nround-trip min/avg/max = 9.9/10.0/10.1 ms\n"), nil
	})
	defer restore()
	_, res, err := pingHandler(context.Background(), nil, pingInput{Host: "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Transmitted != 4 || res.Received != 4 || res.LossPct != 0 || res.AvgMs != 10.0 {
		t.Fatalf("ping result wrong: %+v", res)
	}
	if res.Raw == "" {
		t.Error("raw excerpt expected")
	}
}

func TestPingHandlerInvalidHostAndCount(t *testing.T) {
	if _, _, err := pingHandler(context.Background(), nil, pingInput{Host: "bad host"}); err == nil {
		t.Error("invalid host must be rejected")
	}
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		// count=99 应钳到 10
		if len(args) >= 2 && args[1] != "10" {
			t.Errorf("count should clamp to 10, got args %v", args)
		}
		return []byte("1 packets transmitted, 1 packets received, 0% packet loss\n"), nil
	})
	defer restore()
	if _, _, err := pingHandler(context.Background(), nil, pingInput{Host: "1.1.1.1", Count: 99}); err != nil {
		t.Fatal(err)
	}
}

func TestPingHandlerAllLost(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("exit status 1: something")
	})
	defer restore()
	if _, _, err := pingHandler(context.Background(), nil, pingInput{Host: "192.0.2.1"}); err == nil {
		t.Fatal("all-lost ping must return error")
	}
}

func TestParseResolvConf(t *testing.T) {
	text := "# Generated by netifd\nnameserver 127.0.0.1\nsearch lan\nnameserver 192.168.100.1\n"
	got := parseResolvConf(text)
	if len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "192.168.100.1" {
		t.Fatalf("got %v", got)
	}
	if got := parseResolvConf(""); len(got) != 0 {
		t.Fatalf("empty wrong: %v", got)
	}
}

func TestRoutesHandler(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		switch name {
		case "ip":
			if len(args) >= 1 && args[0] == "-6" {
				return []byte("fe80::/64 dev eth0  proto kernel metric 256\n"), nil
			}
			return []byte("default via 192.168.1.1 dev eth0\n192.168.1.0/24 dev eth0 scope link\n"), nil
		}
		return nil, errors.New("unexpected: " + name)
	})
	defer restore()
	_, res, err := routesHandler(context.Background(), nil, routesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.IPv4, "default via 192.168.1.1") {
		t.Errorf("ipv4 wrong: %q", res.IPv4)
	}
	if !strings.Contains(res.IPv6, "fe80::") {
		t.Errorf("ipv6 wrong: %q", res.IPv6)
	}
}

func TestRoutesHandlerIPv6FailureIgnored(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "ip" && args[0] == "-6" {
			return nil, errors.New("exit status 1")
		}
		return []byte("default via 192.168.1.1 dev eth0\n"), nil
	})
	defer restore()
	_, res, err := routesHandler(context.Background(), nil, routesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IPv6 != "" || res.Note == "" {
		t.Fatalf("ipv6 failure should be noted: %+v", res)
	}
}
