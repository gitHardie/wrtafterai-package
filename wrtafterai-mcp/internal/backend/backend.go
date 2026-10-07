// Package backend：路由器数据面。全部通过 exec 调用 ubus/uci（参数数组，不经 shell）与直接读 /proc。
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ---- 通用 exec ----

// execRunner 是可替换的命令执行入口（单测注入假输出用）。
var execRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := execRunner(c, name, args...)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(ee.Stderr))
		}
		return "", fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(b), nil
}

// Run 供 tools 层执行只读诊断命令（iwinfo / logread / speedtest-cli 等）。
func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return run(ctx, timeout, name, args...)
}

// UbusCall 调用 ubus（obj/method 由代码写死，不来自用户输入）。
func UbusCall(ctx context.Context, obj, method string, params any, out any) error {
	args := []string{"call", obj, method}
	if params != nil {
		j, err := json.Marshal(params)
		if err != nil {
			return err
		}
		args = append(args, string(j))
	}
	s, err := run(ctx, 8*time.Second, "ubus", args...)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(s), out)
}

// UCIGet 读取单个 uci 值，空值返回 ""。
func UCIGet(ctx context.Context, opt string) (string, error) {
	s, err := run(ctx, 5*time.Second, "uci", "-q", "get", opt)
	return strings.TrimSpace(s), err
}

// UCIShow 导出 uci 配置文本（只读诊断用）。
func UCIShow(ctx context.Context, pkg string) (string, error) {
	s, err := run(ctx, 8*time.Second, "uci", "-q", "show", pkg)
	return s, err
}

// ---- /proc 与 /sys ----

// SystemBoard 对应 ubus system board。
type SystemBoard struct {
	Kernel    string `json:"kernel"`
	Hostname  string `json:"hostname"`
	Model     string `json:"model"`
	BoardName string `json:"board_name"`
	Release   struct {
		Version      string `json:"version"`
		Distribution string `json:"distribution"`
		Revision     string `json:"revision"`
		Description  string `json:"description"`
	} `json:"release"`
}

func GetSystemBoard(ctx context.Context) (*SystemBoard, error) {
	var b SystemBoard
	if err := UbusCall(ctx, "system", "board", nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// SystemInfo 对应 ubus system info（uptime、内存、负载）。
type SystemInfo struct {
	Uptime int64 `json:"uptime"`
	Memory struct {
		Total     uint64 `json:"total"`
		Free      uint64 `json:"free"`
		Buffered  uint64 `json:"buffered"`
		Shared    uint64 `json:"shared"`
		Cached    uint64 `json:"cached"`
		Available uint64 `json:"available"`
	} `json:"memory"`
	Load []float64 `json:"load"`
}

func GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	var i SystemInfo
	if err := UbusCall(ctx, "system", "info", nil, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

func Temperature(ctx context.Context) []float64 {
	var out []float64
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		b, err := os.ReadFile("/sys/class/thermal/" + e.Name() + "/temp")
		if err != nil {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			out = append(out, v/1000)
		}
	}
	return out
}

// ConntrackStats 连接跟踪统计（读 /proc/net/nf_conntrack）。
type ConntrackStats struct {
	Total       int            `json:"total"`
	ByProto     map[string]int `json:"by_proto"`
	Established int            `json:"established"`
	MaxEntries  int            `json:"max_entries,omitempty"`
}

func GetConntrackStats() (*ConntrackStats, error) {
	b, err := os.ReadFile("/proc/net/nf_conntrack")
	if err != nil {
		return nil, err
	}
	st := &ConntrackStats{ByProto: map[string]int{}}
	if mb, err := os.ReadFile("/proc/sys/net/netfilter/nf_conntrack_max"); err == nil {
		st.MaxEntries, _ = strconv.Atoi(strings.TrimSpace(string(mb)))
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		proto := strings.TrimSuffix(strings.TrimPrefix(f[2], "protocol="), " ")
		st.ByProto[proto]++
		st.Total++
		if strings.Contains(line, "ASSURED") || strings.Contains(line, "ESTABLISHED") {
			st.Established++
		}
	}
	return st, nil
}

// IfaceCounters 网口收发字节数。
type IfaceCounters struct {
	Iface   string `json:"iface"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

func NetDev() ([]IfaceCounters, error) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	var out []IfaceCounters
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out = append(out, IfaceCounters{Iface: strings.TrimSpace(name), RxBytes: rx, TxBytes: tx})
	}
	return out, nil
}

// ---- DHCP ----

// Lease dnsmasq 租约：expiry mac ip hostname clientid。
type Lease struct {
	Expiry   int64  `json:"expiry_ts"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

func DHCPLeases() ([]Lease, error) {
	b, err := os.ReadFile("/tmp/dhcp.leases")
	if err != nil {
		if os.IsNotExist(err) {
			return []Lease{}, nil
		}
		return nil, err
	}
	var out []Lease
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		exp, _ := strconv.ParseInt(f[0], 10, 64)
		out = append(out, Lease{Expiry: exp, MAC: f[1], IP: f[2], Hostname: f[3]})
	}
	return out, nil
}

// ---- ARP ----

// ARPEntry /proc/net/arp 条目（合并设备清单用）。
type ARPEntry struct {
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
	Dev   string `json:"dev"`
	Flags string `json:"flags"` // 0x2=ATF_COM(有效) 0x0=incomplete；ip neigh 文本则可能为 REACHABLE
}

// Online 条目是否为有效邻居（flags 含 ATF_COM 0x2 或 REACHABLE）。
func (a ARPEntry) Online() bool {
	return strings.Contains(a.Flags, "0x2") || strings.Contains(strings.ToUpper(a.Flags), "REACHABLE")
}

func ARPTable() ([]ARPEntry, error) {
	b, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	var out []ARPEntry
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 || f[3] == "00:00:00:00:00:00" {
			continue
		}
		out = append(out, ARPEntry{IP: f[0], MAC: f[3], Dev: f[5], Flags: f[2]})
	}
	return out, nil
}

// ---- 测试钩子 ----

// SetExecRunner 替换命令执行入口并返回恢复函数，仅供单测注入假输出；生产代码不得调用。
func SetExecRunner(fn func(ctx context.Context, name string, args []string) ([]byte, error)) func() {
	old := execRunner
	execRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return fn(ctx, name, args)
	}
	return func() { execRunner = old }
}
