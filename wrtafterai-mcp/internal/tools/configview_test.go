package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
)

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"password", "PASSWORD", "Password", // password 各形态
		"psk", "PSK", "wpa_psk", // psk
		"key", "KEY", "Key", // key
		"serverkey", "private_key", "keymanagement", // key 子串
		"secret", "SECRET", "client_secret", // secret
		"token", "TOKEN", "api_token", // token
		"monkey", "keystone", // 含 key 子串的误报按规范仍脱敏
		"wireless.@wifi-iface[0].key", // 真实 uci 键
		"network.lan.password",        // 真实 uci 键
	}
	for _, k := range sensitive {
		if !isSensitiveKey(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	normal := []string{
		"ssid", "channel", "encryption", "device", "mode", "network",
		"proto", "ipaddr", "netmask", "gateway", "dns", "keepalive",
		"passphrase", // 不含五个敏感子串
	}
	for _, k := range normal {
		if isSensitiveKey(k) {
			t.Errorf("%q should NOT be sensitive", k)
		}
	}
}

func TestMaskSensitiveLines(t *testing.T) {
	text := `network.lan=interface
network.lan.proto='static'
network.lan.ipaddr='192.168.1.1'
network.lan.password='s3cret-pw'
wireless.radio0='wifi-device'
wireless.radio0.channel='36'
wireless.default_radio0='wifi-iface'
wireless.default_radio0.key='MyWiFiPass123'
wireless.default_radio0.ssid='Home'
dhcp.@dnsmasq[0].server='/pool/10.0.0.1'
`
	masked, n := maskSensitiveLines(text)
	if n != 2 {
		t.Fatalf("masked count = %d, want 2", n)
	}
	if !strings.Contains(masked, "network.lan.password=***") {
		t.Errorf("password line not masked: %s", masked)
	}
	if !strings.Contains(masked, "wireless.default_radio0.key=***") {
		t.Errorf("key line not masked: %s", masked)
	}
	if strings.Contains(masked, "s3cret-pw") || strings.Contains(masked, "MyWiFiPass123") {
		t.Errorf("secret value leaked: %s", masked)
	}
	// 非敏感行保持原样
	if !strings.Contains(masked, "network.lan.ipaddr='192.168.1.1'") {
		t.Errorf("normal line should be untouched")
	}
	if !strings.Contains(masked, "wireless.default_radio0.ssid='Home'") {
		t.Errorf("ssid should be untouched")
	}
}

func TestMaskSensitiveLinesEmpty(t *testing.T) {
	masked, n := maskSensitiveLines("")
	if masked != "" || n != 0 {
		t.Fatalf("empty wrong: %q %d", masked, n)
	}
}

func TestParseUCISectionsOf(t *testing.T) {
	text := `wireless.radio0='wifi-device'
wireless.radio0.type='mac80211'
wireless.radio0.channel='36'
wireless.radio0.band='5g'
wireless.radio0.htmode='HE80'
wireless.radio0.disabled='0'
wireless.default_radio0='wifi-iface'
wireless.default_radio0.device='radio0'
wireless.default_radio0.network='lan'
wireless.default_radio0.mode='ap'
wireless.default_radio0.ssid='HomeNet'
wireless.default_radio0.encryption='psk2'
wireless.default_radio0.key='secret123'
wireless.radio1='wifi-device'
wireless.radio1.type='mac80211'
# 注释行
garbage_without_equals
network.lan='interface'
`
	secs := parseUCISectionsOf(text, "wireless")
	if len(secs) != 3 {
		t.Fatalf("got %d sections, want 3 (network.lan must be excluded): %v", len(secs), keysOf(secs))
	}
	r0 := secs["radio0"]
	if r0 == nil || r0["type"] != "mac80211" || r0[secTypeKey] != "wifi-device" || r0["channel"] != "36" || r0["band"] != "5g" || r0["htmode"] != "HE80" {
		t.Fatalf("radio0 wrong: %+v", r0)
	}
	iface := secs["default_radio0"]
	if iface == nil || iface["ssid"] != "HomeNet" || iface["encryption"] != "psk2" || iface["key"] != "secret123" {
		t.Fatalf("iface wrong: %+v", iface)
	}
	if secs["radio1"][secTypeKey] != "wifi-device" || secs["radio1"]["type"] != "mac80211" {
		t.Fatalf("radio1 wrong: %+v", secs["radio1"])
	}
}

func TestParseUCISectionsOfEmpty(t *testing.T) {
	if secs := parseUCISectionsOf("", "dhcp"); len(secs) != 0 {
		t.Fatalf("empty wrong: %v", secs)
	}
}

func TestSplitUCIValues(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"'a' 'b' 'c'", []string{"a", "b", "c"}},
		{"'/pool/10.0.0.1'", []string{"/pool/10.0.0.1"}},
		{"plain", []string{"plain"}},
		{`"quoted"`, []string{"quoted"}},
		{"", nil},
		{"''", nil},
	}
	for _, c := range cases {
		got := splitUCIValues(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitUCIValues(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitUCIValues(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestGetConfigHandlerMaskAndTruncate(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "uci" {
			return nil, errors.New("unexpected: " + name)
		}
		joined := strings.Join(args, " ")
		if joined != "-q show network.lan" {
			t.Errorf("uci args = %q", joined)
		}
		return []byte("network.lan.proto='static'\nnetwork.lan.password='topsecret'\nnetwork.lan.ipaddr='192.168.1.1'\n"), nil
	})
	defer restore()

	_, res, err := getConfigHandler(context.Background(), nil, getConfigInput{Package: "network", Section: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MaskedCount != 1 {
		t.Fatalf("masked = %d, want 1", res.MaskedCount)
	}
	if strings.Contains(res.Config, "topsecret") {
		t.Fatal("secret leaked!")
	}
	if !strings.Contains(res.Config, "network.lan.password=***") {
		t.Fatalf("masked line missing: %s", res.Config)
	}
	if !strings.Contains(res.Config, "network.lan.ipaddr='192.168.1.1'") {
		t.Fatalf("normal line missing: %s", res.Config)
	}
}

func TestGetConfigHandlerInvalidInput(t *testing.T) {
	if _, _, err := getConfigHandler(context.Background(), nil, getConfigInput{Package: "bad;pkg"}); err == nil {
		t.Error("shell metachars in package must be rejected")
	}
	if _, _, err := getConfigHandler(context.Background(), nil, getConfigInput{Package: "network", Section: "bad section"}); err == nil {
		t.Error("space in section must be rejected")
	}
	// 合法 @type[index] 形式应通过校验（uci 不存在该包会返回错误，但不是入参校验错误）
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("uci: Entry not found")
	})
	defer restore()
	if _, _, err := getConfigHandler(context.Background(), nil, getConfigInput{Package: "firewall", Section: "@zone[0]"}); err == nil {
		t.Error("missing package should error from uci, and @zone[0] should pass input validation")
	}
}

func TestWirelessConfigHandler(t *testing.T) {
	raw := "wireless.radio0='wifi-device'\n" +
		"wireless.radio0.type='mac80211'\nwireless.radio0.channel='auto'\n" +
		"wireless.radio0.band='2g'\nwireless.radio0.htmode='HT20'\n" +
		"wireless.radio0.txpower='20'\nwireless.radio0.disabled='0'\n" +
		"wireless.default_radio0='wifi-iface'\nwireless.default_radio0.device='radio0'\n" +
		"wireless.default_radio0.ssid='HomeNet'\nwireless.default_radio0.mode='ap'\n" +
		"wireless.default_radio0.network='lan'\nwireless.default_radio0.encryption='psk2'\n" +
		"wireless.default_radio0.key='wifipass'\n"
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "uci" || strings.Join(args, " ") != "-q show wireless" {
			return nil, errors.New("unexpected: " + strings.Join(args, " "))
		}
		return []byte(raw), nil
	})
	defer restore()

	_, res, err := wirelessConfigHandler(context.Background(), nil, wirelessConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceCount != 1 || res.IfaceCount != 1 {
		t.Fatalf("counts wrong: %+v", res)
	}
	d := res.Devices[0]
	if d.Name != "radio0" || d.Channel != "auto" || d.Band != "2g" || d.Htmode != "HT20" || d.Txpower != "20" || d.Disabled {
		t.Errorf("device wrong: %+v", d)
	}
	i := res.Ifaces[0]
	if i.Ssid != "HomeNet" || i.Mode != "ap" || i.Network != "lan" || i.Encryption != "psk2" || i.Device != "radio0" {
		t.Errorf("iface wrong: %+v", i)
	}
	// 摘录脱敏
	if res.MaskedCount != 1 || !strings.Contains(res.Excerpt, "default_radio0.key=***") || strings.Contains(res.Excerpt, "wifipass") {
		t.Errorf("excerpt masking wrong: masked=%d excerpt=%s", res.MaskedCount, res.Excerpt)
	}
}

func TestWirelessConfigHandlerEmpty(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte(""), nil
	})
	defer restore()
	_, res, err := wirelessConfigHandler(context.Background(), nil, wirelessConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Note == "" || res.DeviceCount != 0 {
		t.Fatalf("empty wireless should note: %+v", res)
	}
}

func TestDNSConfigHandler(t *testing.T) {
	withFakeProc(t, map[string]string{
		"/tmp/resolv.conf.d/resolv.conf": "nameserver 127.0.0.1\nnameserver 192.168.1.254\n",
	})
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "uci" || strings.Join(args, " ") != "-q show dhcp" {
			return nil, errors.New("unexpected: " + strings.Join(args, " "))
		}
		return []byte("dhcp.@dnsmasq[0]='dnsmasq'\ndhcp.@dnsmasq[0].noresolv='1'\n" +
			"dhcp.@dnsmasq[0].server='/pool/10.0.0.1' '8.8.8.8'\n" +
			"dhcp.@dnsmasq[0].address='/my.lan/192.168.1.1'\n" +
			"dhcp.lan='dhcp'\n"), nil
	})
	defer restore()

	_, res, err := dnsConfigHandler(context.Background(), nil, dnsConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ResolvFile != "/tmp/resolv.conf.d/resolv.conf" {
		t.Errorf("resolv file = %q", res.ResolvFile)
	}
	if len(res.ResolvServers) != 2 {
		t.Errorf("resolv servers = %v", res.ResolvServers)
	}
	if len(res.UpstreamServers) != 2 || res.UpstreamServers[0] != "/pool/10.0.0.1" || res.UpstreamServers[1] != "8.8.8.8" {
		t.Errorf("upstream = %v", res.UpstreamServers)
	}
	if len(res.StaticEntries) != 1 || res.StaticEntries[0] != "/my.lan/192.168.1.1" {
		t.Errorf("static entries = %v", res.StaticEntries)
	}
	if !res.Noresolv {
		t.Error("noresolv should be true")
	}
}

func TestDNSConfigHandlerResolvFallback(t *testing.T) {
	// 新版路径不存在 → 落到 /tmp/resolv.conf
	withFakeProc(t, map[string]string{
		"/tmp/resolv.conf": "nameserver 10.0.0.2\n",
	})
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("uci: Entry not found")
	})
	defer restore()
	_, res, err := dnsConfigHandler(context.Background(), nil, dnsConfigInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ResolvFile != "/tmp/resolv.conf" || len(res.ResolvServers) != 1 {
		t.Fatalf("fallback wrong: %+v", res)
	}
	if res.Note == "" {
		t.Error("uci failure should be noted")
	}
}

func TestDNSConfigHandlerAllFailed(t *testing.T) {
	withFakeProc(t, map[string]string{})
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("boom")
	})
	defer restore()
	if _, _, err := dnsConfigHandler(context.Background(), nil, dnsConfigInput{}); err == nil {
		t.Fatal("both sources failing must error")
	}
}
