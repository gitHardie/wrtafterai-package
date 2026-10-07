package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
)

// withFakeProc 注入 procReader，按路径返回假文件内容。
func withFakeProc(t *testing.T, files map[string]string) {
	t.Helper()
	old := procReader
	procReader = func(path string) ([]byte, error) {
		if s, ok := files[path]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("no such file: " + path)
	}
	t.Cleanup(func() { procReader = old })
}

func TestSystemResourcesHandler(t *testing.T) {
	withFakeProc(t, map[string]string{
		"/proc/loadavg": "0.52 0.58 0.59 2/489 1234",
		"/proc/cpuinfo": "processor\t: 0\nmodel name\t: ARMv7 Processor rev 5\nprocessor\t: 1\nprocessor\t: 2\nprocessor\t: 3\n",
		"/proc/meminfo": "MemTotal:        453408 kB\nMemFree:          102400 kB\nMemAvailable:     307200 kB\nBuffers:           10240 kB\nCached:           122880 kB\nSwapTotal:        131072 kB\nSwapFree:         131072 kB\n",
	})
	// cpufreq 缺失 → FreqMHz 保持 0，不报错
	_, res, err := systemResourcesHandler(context.Background(), nil, systemResourcesInput{})
	if err != nil {
		t.Fatalf("systemResourcesHandler: %v", err)
	}
	out := res
	if out.Load != [3]float64{0.52, 0.58, 0.59} {
		t.Errorf("load wrong: %v", out.Load)
	}
	if out.CPU.Cores != 4 || out.CPU.Model != "ARMv7 Processor rev 5" {
		t.Errorf("cpu wrong: %+v", out.CPU)
	}
	if out.CPU.FreqMHz != 0 {
		t.Errorf("missing cpufreq should give 0, got %v", out.CPU.FreqMHz)
	}
	if out.Mem.TotalMB != 442.78125 || out.Mem.AvailableMB != 300 {
		t.Errorf("mem wrong: %+v", out.Mem)
	}
	if out.Mem.UsedPct <= 0 || out.Mem.UsedPct >= 100 {
		t.Errorf("mem used pct wrong: %v", out.Mem.UsedPct)
	}
	if out.Swap.TotalMB != 128 || out.Swap.FreeMB != 128 {
		t.Errorf("swap wrong: %+v", out.Swap)
	}
	if out.Note != "" {
		t.Errorf("expected no note, got %q", out.Note)
	}
}

func TestSystemResourcesCpufreqMHz(t *testing.T) {
	withFakeProc(t, map[string]string{
		"/proc/loadavg": "0.10 0.20 0.30 1/2 3",
		"/proc/cpuinfo": "processor\t: 0\nHardware\t: Generic DT based machine\n",
		"/proc/meminfo": "MemTotal: 102400 kB\nMemFree: 51200 kB\n",
		"/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq": "1200000\n",
	})
	_, res, err := systemResourcesHandler(context.Background(), nil, systemResourcesInput{})
	if err != nil {
		t.Fatal(err)
	}
	out := res
	if out.CPU.FreqMHz != 1200 {
		t.Errorf("freq = %v, want 1200 MHz", out.CPU.FreqMHz)
	}
	if out.CPU.Model != "Generic DT based machine" {
		t.Errorf("fallback model wrong: %q", out.CPU.Model)
	}
	// 老内核无 MemAvailable → 用 MemFree 兜底
	if out.Mem.AvailableMB != 50 {
		t.Errorf("mem available fallback = %v, want 50", out.Mem.AvailableMB)
	}
}

func TestSystemResourcesAllProcFailed(t *testing.T) {
	withFakeProc(t, map[string]string{}) // 全部读取失败
	_, res, err := systemResourcesHandler(context.Background(), nil, systemResourcesInput{})
	if err != nil {
		t.Fatalf("/proc 全部失败应优雅降级而非报错: %v", err)
	}
	if res.Note == "" {
		t.Fatal("expected failure notes in Note")
	}
	if !strings.Contains(res.Note, "loadavg") || !strings.Contains(res.Note, "meminfo") {
		t.Errorf("note should mention failures: %q", res.Note)
	}
}

func TestParseLoadavg(t *testing.T) {
	if _, _, _, ok := parseLoadavg("garbage"); ok {
		t.Fatal("garbage should not parse")
	}
	if _, _, _, ok := parseLoadavg("0.1"); ok {
		t.Fatal("short line should not parse")
	}
	l1, l5, l15, ok := parseLoadavg("  1.23 4.56 7.89 2/100 999 ")
	if !ok || l1 != 1.23 || l5 != 4.56 || l15 != 7.89 {
		t.Fatalf("got %v %v %v %v", l1, l5, l15, ok)
	}
}

func TestParseDfOutput(t *testing.T) {
	text := `Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/root               262144    123456    138688  47% /
tmpfs                   258956      1234    257722   0% /tmp
tmpfs                      512        16       496   3% /dev
proc                         0         0         0   0% /proc
sysfs                        0         0         0   0% /sys
cgroup2                      0         0         0   0% /sys/fs/cgroup
devtmpfs                  1024         0      1024   0% /dev
overlay:/overlay       524288    234567    289721  45% /mnt/overlay
/dev/sda1            31234624   1234567  29999999   4% /mnt/usb1
`
	rows := parseDfOutput(text)
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (proc/sysfs/cgroup2/devtmpfs filtered, tmpfs kept): %+v", len(rows), rows)
	}
	r0 := rows[0]
	if r0.Filesystem != "/dev/root" || r0.Mount != "/" || !r0.Root {
		t.Fatalf("root row wrong: %+v", r0)
	}
	if r0.SizeMB != 256 || r0.UsedMB != 120.5625 || r0.UsePct != 47 {
		t.Errorf("root numbers wrong: %+v", r0)
	}
	tmp := rows[1]
	if tmp.Filesystem != "tmpfs" || tmp.Mount != "/tmp" {
		t.Errorf("tmpfs row wrong: %+v", tmp)
	}
	ovl := rows[3]
	if ovl.Filesystem != "overlay:/overlay" || !ovl.External {
		t.Errorf("overlay external row wrong: %+v", ovl)
	}
	usb := rows[4]
	if usb.Filesystem != "/dev/sda1" || !usb.External || usb.Mount != "/mnt/usb1" {
		t.Errorf("usb row wrong: %+v", usb)
	}
}

func TestParseDfOutputWrappedDevice(t *testing.T) {
	// 长设备名单独成行的 GNU df 风格
	text := `Filesystem            1K-blocks    Used Available Use% Mounted on
very-long-device-name-from-network-1
                        1048576  524288    524288  50% /mnt/nfs
`
	rows := parseDfOutput(text)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Filesystem != "very-long-device-name-from-network-1" || rows[0].UsePct != 50 {
		t.Fatalf("wrapped row wrong: %+v", rows[0])
	}
}

func TestParseDfOutputGarbage(t *testing.T) {
	if rows := parseDfOutput(""); len(rows) != 0 {
		t.Fatalf("empty input should give 0 rows, got %d", len(rows))
	}
	if rows := parseDfOutput("Filesystem 1K-blocks Used Available Use% Mounted on\nnot a df line\n"); len(rows) != 0 {
		t.Fatalf("garbage should give 0 rows, got %d", len(rows))
	}
}

func TestDiskUsageHandlerViaFakeExec(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name != "df" || len(args) != 1 || args[0] != "-k" {
			return nil, errors.New("unexpected call: " + name)
		}
		return []byte("Filesystem 1K-blocks Used Available Use% Mounted on\n/dev/root 262144 123456 138688 47% /\ntmpfs 258956 1234 257722 0% /tmp\n"), nil
	})
	defer restore()

	_, res, err := diskUsageHandler(context.Background(), nil, diskUsageInput{})
	if err != nil {
		t.Fatalf("diskUsageHandler: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("count = %d, want 2", res.Count)
	}
	// 去重后总量：/dev/root 262144KB + tmpfs 258956KB
	if res.TotalMB != float64(262144+258956)/1024 {
		t.Errorf("total = %v", res.TotalMB)
	}
}

func TestDiskUsageHandlerDfFailed(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	})
	defer restore()
	if _, _, err := diskUsageHandler(context.Background(), nil, diskUsageInput{}); err == nil {
		t.Fatal("df failure must return error")
	}
}

func TestParseTopOutput(t *testing.T) {
	text := `Mem: 57304K used, 395604K free, 160K shrd, 1664K buff, 138676K cached
CPU:  0% usr  4% sys  0% nic 95% idle  0% io  0% irq  0% sirq
Load average: 0.52 0.58 0.59 2/489 1234
  PID  PPID USER     STAT   VSZ %VSZ %CPU COMMAND
 1234     1 root     S     12345   2%  12% nginx: master
  888     1 root     S      5678   1%   5% dnsmasq
  999  1234 root     R      2345   0%  20% curl
`
	parsed, ok := parseTopOutput(text)
	if !ok {
		t.Fatal("should parse")
	}
	if parsed.MemUsedKB != 57304 || parsed.MemFreeKB != 395604 {
		t.Errorf("mem wrong: %d %d", parsed.MemUsedKB, parsed.MemFreeKB)
	}
	if parsed.CpuIdlePct != 95 {
		t.Errorf("idle = %v, want 95", parsed.CpuIdlePct)
	}
	if len(parsed.Procs) != 3 {
		t.Fatalf("got %d procs, want 3: %+v", len(parsed.Procs), parsed.Procs)
	}
	// 按 CPU 降序
	if parsed.Procs[0].PID != 999 || parsed.Procs[0].CpuPct != 20 {
		t.Errorf("sorted[0] wrong: %+v", parsed.Procs[0])
	}
	if parsed.Procs[1].PID != 1234 || parsed.Procs[1].CpuPct != 12 {
		t.Errorf("sorted[1] wrong: %+v", parsed.Procs[1])
	}
	// COMMAND 含空格合并
	if parsed.Procs[1].Command != "nginx: master" {
		t.Errorf("command = %q", parsed.Procs[0].Command)
	}
	if parsed.Procs[2].User != "root" || parsed.Procs[2].VszKB != 5678 {
		t.Errorf("row fields wrong: %+v", parsed.Procs[1])
	}
}

func TestParseTopOutputNoPPIDColumn(t *testing.T) {
	text := `Mem: 1000K used, 2000K free
  PID USER     STAT   VSZ %VSZ %CPU COMMAND
    1 root     S       100   1%   3% init
`
	parsed, ok := parseTopOutput(text)
	if !ok || len(parsed.Procs) != 1 || parsed.Procs[0].Command != "init" {
		t.Fatalf("no-PPID variant failed: ok=%v procs=%+v", ok, parsed.Procs)
	}
}

func TestParseTopOutputGarbage(t *testing.T) {
	if _, ok := parseTopOutput("not a top output at all"); ok {
		t.Fatal("garbage should fail")
	}
	if _, ok := parseTopOutput(""); ok {
		t.Fatal("empty should fail")
	}
}

func TestTopProcessesHandlerFallback(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte("completely unexpected output\nline two\n"), nil
	})
	defer restore()
	_, res, err := topProcessesHandler(context.Background(), nil, topProcessesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Raw == "" || res.Note == "" {
		t.Fatalf("expected raw fallback with note, got note=%q raw=%q", res.Note, res.Raw)
	}
}

func TestTopProcessesHandlerCountClamp(t *testing.T) {
	restore := backend.SetExecRunner(func(ctx context.Context, name string, args []string) ([]byte, error) {
		var sb strings.Builder
		sb.WriteString("  PID  PPID USER     STAT   VSZ %VSZ %CPU COMMAND\n")
		for i := 0; i < 15; i++ {
			sb.WriteString(strings.Repeat(" ", 5) + itoa(100+i) + "     1 root     S      100   1%   1% proc" + itoa(i) + "\n")
		}
		return []byte(sb.String()), nil
	})
	defer restore()

	// 默认 10
	_, res, _ := topProcessesHandler(context.Background(), nil, topProcessesInput{})
	if res.Count != 10 {
		t.Errorf("default count = %d, want 10", res.Count)
	}
	// 超上限截到 20（这里只有 15 行进程）
	_, res, _ = topProcessesHandler(context.Background(), nil, topProcessesInput{Count: 50})
	if res.Count != 15 {
		t.Errorf("clamped count = %d, want 15 (all available)", res.Count)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
