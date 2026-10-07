package tools

import "testing"

func TestTruncateLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"empty", "", 5, ""},
		{"fewer than max", "a\nb\nc", 5, "a\nb\nc"},
		{"exact max", "a\nb\nc", 3, "a\nb\nc"},
		{"over max keeps tail", "a\nb\nc", 2, "b\nc"},
		{"max one", "a\nb\nc\nd\ne", 1, "e"},
		{"single line", "single", 10, "single"},
		{"max zero keeps nothing", "a\nb", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncateLines(c.in, c.max); got != c.want {
				t.Fatalf("truncateLines(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}
}

func TestParseUCISections(t *testing.T) {
	text := `firewall.@zone[0]=zone
firewall.@zone[0].name='wan'
firewall.@zone[0].input='REJECT'
firewall.@zone[0].forward='DROP'
firewall.@rule[2]=rule
firewall.@rule[2].name='Allow-SSH'
firewall.@rule[2].src='wan'
firewall.@rule[2].dest_port='22'
firewall.@rule[2].target='ACCEPT'
firewall.@rule[2].enabled='0'
firewall.mysec=rule
firewall.mysec.dest='lan'
firewall.mysec.target='DROP'
`
	secs := parseUCISections(text)
	if len(secs) != 3 {
		t.Fatalf("got %d sections, want 3: %v", len(secs), keysOf(secs))
	}
	z := secs["@zone[0]"]
	if z == nil || z["type"] != "zone" || z["name"] != "wan" || z["input"] != "REJECT" || z["forward"] != "DROP" {
		t.Fatalf("zone section wrong: %+v", z)
	}
	r := secs["@rule[2]"]
	if r == nil || r["type"] != "rule" || r["name"] != "Allow-SSH" || r["enabled"] != "0" {
		t.Fatalf("rule section wrong: %+v", r)
	}
	m := secs["mysec"]
	if m == nil || m["type"] != "rule" || m["dest"] != "lan" || m["target"] != "DROP" {
		t.Fatalf("named section wrong: %+v", m)
	}
}

func keysOf(m map[string]map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParseIwinfoAssoc(t *testing.T) {
	text := `AA:BB:CC:DD:EE:FF  -52 dBm / -95 dBm (SNR 43)  120 ms ago
	RX: 130.0 MBit/s, MCS 14, 20 MHz           1032 Pkts.
	TX: 260.0 MBit/s, MCS 15, 40 MHz           2048 Pkts.
11:22:33:44:55:66  -70 dBm / -95 dBm (SNR 25)  800 ms ago
	RX: 54.0 MBit/s                            10 Pkts.
	TX: 54.0 MBit/s                            12 Pkts.
`
	clients := parseIwinfoAssoc(text)
	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2", len(clients))
	}
	c0 := clients[0]
	if c0.MAC != "aa:bb:cc:dd:ee:ff" || c0.Signal != -52 || c0.Noise != -95 {
		t.Fatalf("client0 wrong: %+v", c0)
	}
	if c0.RxMbps != 130.0 || c0.TxMbps != 260.0 {
		t.Fatalf("client0 rates wrong: rx=%v tx=%v", c0.RxMbps, c0.TxMbps)
	}
	c1 := clients[1]
	if c1.MAC != "11:22:33:44:55:66" || c1.Signal != -70 || c1.Noise != -95 {
		t.Fatalf("client1 wrong: %+v", c1)
	}
	if c1.RxMbps != 54.0 || c1.TxMbps != 54.0 {
		t.Fatalf("client1 rates wrong: %+v", c1)
	}
}

func TestParseIwinfoAssocGarbage(t *testing.T) {
	if got := parseIwinfoAssoc(""); len(got) != 0 {
		t.Fatalf("empty input should give 0 clients, got %d", len(got))
	}
	if got := parseIwinfoAssoc("not a mac line at all\nrx garbage"); len(got) != 0 {
		t.Fatalf("garbage should give 0 clients, got %d", len(got))
	}
}

func TestIsMAC(t *testing.T) {
	if !isMAC("AA:BB:CC:DD:EE:FF") || !isMAC("00:11:22:33:44:55") {
		t.Fatal("valid mac rejected")
	}
	for _, s := range []string{"", "AA:BB:CC:DD:EE", "AA-BB-CC-DD-EE-FF", "XX:BB:CC:DD:EE:FF", "AA:BB:CC:DD:EE:FF:00"} {
		if isMAC(s) {
			t.Fatalf("%q should not be a mac", s)
		}
	}
}

func TestIwinfoDevices(t *testing.T) {
	raw := `wlan0       ESSID: "HomeWiFi"
          Access Point: 66:70:aa:bb:cc:dd
          Mode: Master  Channel: 6 (2.437 GHz)
wlan1       ESSID: unknown
          Mode: Master  Channel: 36
eth0        Link: yes
`
	devs := iwinfoDevices(raw)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(devs), devs)
	}
	if devs[0].Name != "wlan0" || devs[0].SSID != "HomeWiFi" {
		t.Fatalf("wlan0 parse wrong: %+v", devs[0])
	}
	if devs[1].Name != "wlan1" || devs[1].SSID != "unknown" {
		t.Fatalf("wlan1 parse wrong: %+v", devs[1])
	}
}

func TestZoneOf(t *testing.T) {
	cases := []struct {
		dev         string
		fallbackLan bool
		want        string
	}{
		{"br-lan", false, "lan"},
		{"wan", false, "wan"},
		{"pppoe-wan", false, "wan"},
		{"eth0.1", false, "eth0.1"},
		{"", true, "lan"},
		{"", false, ""},
	}
	for _, c := range cases {
		if got := zoneOf(c.dev, c.fallbackLan); got != c.want {
			t.Fatalf("zoneOf(%q, %v) = %q, want %q", c.dev, c.fallbackLan, got, c.want)
		}
	}
}

func TestDelta(t *testing.T) {
	if delta(100, 50) != 50 {
		t.Fatal("normal delta wrong")
	}
	if delta(50, 100) != 0 {
		t.Fatal("counter reset must yield 0, not negative")
	}
}
