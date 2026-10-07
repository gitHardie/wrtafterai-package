// resources 域：router.system_resources / router.disk_usage / router.top_processes。
// 数据源：/proc 与 /sys 直读 + df/top 命令（backend.Run，参数数组）。
package tools

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// procReader 是 /proc、/sys 及 resolv.conf 等本地文件的读取入口，
// 抽成包级 var 以便单测注入假数据（生产路径为 os.ReadFile）。
var procReader = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ---- router.system_resources ----

type cpuResourceOut struct {
	Cores   int     `json:"cores"`
	Model   string  `json:"model,omitempty"`
	FreqMHz float64 `json:"freq_mhz,omitempty"` // cpu0 当前频率，缺失时省略
}

type memResourceOut struct {
	TotalMB     float64 `json:"total_mb"`
	AvailableMB float64 `json:"available_mb,omitempty"`
	UsedPct     float64 `json:"used_pct,omitempty"`
}

type swapResourceOut struct {
	TotalMB float64 `json:"total_mb"`
	FreeMB  float64 `json:"free_mb"`
	UsedPct float64 `json:"used_pct,omitempty"`
}

type systemResourcesOutput struct {
	CPU   cpuResourceOut  `json:"cpu"`
	Mem   memResourceOut  `json:"mem"`
	Swap  swapResourceOut `json:"swap"`
	Load  [3]float64      `json:"load"`
	TempC []float64       `json:"temp_c,omitempty"`
	Note  string          `json:"note,omitempty"`
}

type systemResourcesInput struct{}

func systemResourcesHandler(ctx context.Context, req *mcp.CallToolRequest, in systemResourcesInput) (*mcp.CallToolResult, systemResourcesOutput, error) {
	var out systemResourcesOutput
	var notes []string

	if b, err := procReader("/proc/loadavg"); err == nil {
		if l1, l5, l15, ok := parseLoadavg(string(b)); ok {
			out.Load = [3]float64{l1, l5, l15}
		}
	} else {
		notes = append(notes, "loadavg 读取失败")
	}

	if b, err := procReader("/proc/cpuinfo"); err == nil {
		out.CPU.Cores, out.CPU.Model = parseCpuinfo(string(b))
	} else {
		notes = append(notes, "cpuinfo 读取失败")
	}
	if b, err := procReader("/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"); err == nil {
		if khz, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil && khz > 0 {
			out.CPU.FreqMHz = khz / 1000
		}
	} // 调频信息缺失属正常（部分平台无 cpufreq），不算 note

	if b, err := procReader("/proc/meminfo"); err == nil {
		mi := parseMeminfo(string(b))
		out.Mem.TotalMB = float64(mi["MemTotal"]) / 1024
		avail := mi["MemAvailable"]
		if avail == 0 {
			avail = mi["MemFree"] // 老内核无 MemAvailable
		}
		out.Mem.AvailableMB = float64(avail) / 1024
		if mi["MemTotal"] > 0 {
			out.Mem.UsedPct = float64(mi["MemTotal"]-avail) * 100 / float64(mi["MemTotal"])
		}
		out.Swap.TotalMB = float64(mi["SwapTotal"]) / 1024
		out.Swap.FreeMB = float64(mi["SwapFree"]) / 1024
		if mi["SwapTotal"] > 0 {
			out.Swap.UsedPct = float64(mi["SwapTotal"]-mi["SwapFree"]) * 100 / float64(mi["SwapTotal"])
		}
	} else {
		notes = append(notes, "meminfo 读取失败")
	}

	out.TempC = backend.Temperature(ctx)
	out.Note = strings.Join(notes, "; ")
	return nil, out, nil
}

// parseLoadavg 解析 "0.52 0.58 0.59 2/489 1234"。
func parseLoadavg(s string) (l1, l5, l15 float64, ok bool) {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) < 3 {
		return 0, 0, 0, false
	}
	var err error
	if l1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return 0, 0, 0, false
	}
	if l5, err = strconv.ParseFloat(f[1], 64); err != nil {
		return 0, 0, 0, false
	}
	if l15, err = strconv.ParseFloat(f[2], 64); err != nil {
		return 0, 0, 0, false
	}
	return l1, l5, l15, true
}

// parseCpuinfo 返回核数与 CPU 型号；model name 缺失时退回 Processor/Hardware 行。
func parseCpuinfo(s string) (cores int, model string) {
	fallback := ""
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case k == "processor":
			cores++
		case k == "model name" && model == "":
			model = v
		case (k == "Processor" || k == "Hardware") && fallback == "":
			fallback = v
		}
	}
	if model == "" {
		model = fallback
	}
	return cores, model
}

// parseMeminfo 提取关心的 kB 值（键保持 /proc/meminfo 原名）。
func parseMeminfo(s string) map[string]uint64 {
	out := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			out[strings.TrimSpace(k)] = n
		}
	}
	return out
}

// ---- router.disk_usage ----

type diskRow struct {
	Filesystem string  `json:"filesystem"`
	Mount      string  `json:"mount"`
	SizeMB     float64 `json:"size_mb"`
	UsedMB     float64 `json:"used_mb"`
	AvailMB    float64 `json:"avail_mb"`
	UsePct     float64 `json:"use_pct"`
	Root       bool    `json:"root,omitempty"`     // 根分区（含 overlay）
	External   bool    `json:"external,omitempty"` // /mnt 等外置挂载
}

type diskUsageOutput struct {
	TotalMB     float64   `json:"total_mb"` // 去重后的文件系统总容量
	UsedMB      float64   `json:"used_mb"`
	Filesystems []diskRow `json:"filesystems,omitempty"`
	Count       int       `json:"count"`
	Note        string    `json:"note,omitempty"`
}

type diskUsageInput struct{}

// pseudoFS 是 df 首列中需要过滤的伪文件系统（tmpfs 保留）。
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "debugfs": true, "tracefs": true,
	"bpf": true, "pstore": true, "securityfs": true, "configfs": true,
	"hugetlbfs": true, "ramfs": true, "fusectl": true, "none": true,
}

func diskUsageHandler(ctx context.Context, req *mcp.CallToolRequest, in diskUsageInput) (*mcp.CallToolResult, diskUsageOutput, error) {
	var out diskUsageOutput
	text, err := backend.Run(ctx, 10*time.Second, "df", "-k")
	if err != nil {
		return nil, out, &simpleError{"df 执行失败: " + briefErr(err)}
	}
	rows := parseDfOutput(text)
	if len(rows) == 0 {
		return nil, out, &simpleError{"df 输出无法解析（格式异常）"}
	}

	// 总量按设备去重（overlay 可能同时挂载多处）
	seen := map[string]bool{}
	for _, r := range rows {
		out.Filesystems = append(out.Filesystems, r)
		if !seen[r.Filesystem] {
			seen[r.Filesystem] = true
			out.TotalMB += r.SizeMB
			out.UsedMB += r.UsedMB
		}
	}
	out.Count = len(rows)
	return nil, out, nil
}

// parseDfOutput 解析 df -k 输出：过滤伪 FS，标记根分区与 /mnt 外置挂载。
// 兼容长设备名换行（设备名单独成行时与下一数据行合并）。
func parseDfOutput(text string) []diskRow {
	var out []diskRow
	var pendingDev string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "Filesystem") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 1 {
			pendingDev = f[0] // 设备名换行场景
			continue
		}
		// 换行合并后数据行只剩 5 列：1K-blocks Used Avail Use% Mount
		if len(f) == 5 && pendingDev != "" {
			dev := pendingDev
			pendingDev = ""
			if pseudoFS[dev] {
				continue
			}
			if r, ok := makeDiskRow(dev, f[0], f[1], f[2], f[3], f[4]); ok {
				out = append(out, r)
			}
			continue
		}
		if len(f) < 6 {
			pendingDev = "" // 无法归类的残行，丢弃
			continue
		}
		dev := f[0]
		if pendingDev != "" {
			dev = pendingDev
			pendingDev = ""
		}
		if pseudoFS[dev] {
			continue
		}
		if r, ok := makeDiskRow(dev, f[1], f[2], f[3], f[4], f[5]); ok {
			out = append(out, r)
		}
	}
	return out
}

// makeDiskRow 从 df 字段构造行（单位 kB→MB），字段解析失败返回 ok=false。
func makeDiskRow(dev, sizeF, usedF, availF, pctF, mount string) (diskRow, bool) {
	size, err1 := strconv.ParseUint(sizeF, 10, 64)
	used, err2 := strconv.ParseUint(usedF, 10, 64)
	avail, err3 := strconv.ParseUint(availF, 10, 64)
	pct, err4 := strconv.ParseFloat(strings.TrimSuffix(pctF, "%"), 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return diskRow{}, false
	}
	return diskRow{
		Filesystem: dev,
		Mount:      mount,
		SizeMB:     float64(size) / 1024,
		UsedMB:     float64(used) / 1024,
		AvailMB:    float64(avail) / 1024,
		UsePct:     pct,
		Root:       mount == "/",
		External:   strings.HasPrefix(mount, "/mnt") || strings.HasPrefix(mount, "/media"),
	}, true
}

// ---- router.top_processes ----

type procRow struct {
	PID     int     `json:"pid"`
	User    string  `json:"user,omitempty"`
	Stat    string  `json:"stat,omitempty"`
	VszKB   uint64  `json:"vsz_kb,omitempty"`
	MemPct  float64 `json:"mem_pct,omitempty"`
	CpuPct  float64 `json:"cpu_pct"`
	Command string  `json:"command"`
}

type topProcessesOutput struct {
	Count      int       `json:"count"`
	MemUsedMB  float64   `json:"mem_used_mb,omitempty"`
	MemFreeMB  float64   `json:"mem_free_mb,omitempty"`
	CpuIdlePct float64   `json:"cpu_idle_pct,omitempty"`
	Processes  []procRow `json:"processes,omitempty"`
	Raw        string    `json:"raw,omitempty"` // 解析失败时的原文截断兜底
	Note       string    `json:"note,omitempty"`
}

type topProcessesInput struct {
	Count int `json:"count,omitempty" jsonschema:"number of processes to return (1-20, default 10)"`
}

func topProcessesHandler(ctx context.Context, req *mcp.CallToolRequest, in topProcessesInput) (*mcp.CallToolResult, topProcessesOutput, error) {
	var out topProcessesOutput
	n := in.Count
	if n <= 0 {
		n = 10
	}
	if n > 20 {
		n = 20
	}

	text, err := backend.Run(ctx, 10*time.Second, "top", "-bn1")
	if err != nil {
		return nil, out, &simpleError{"top 执行失败: " + briefErr(err)}
	}
	parsed, ok := parseTopOutput(text)
	if !ok {
		out.Note = "top 输出解析失败，返回原文截断"
		out.Raw = truncateLines(text, 40)
		return nil, out, nil
	}
	out.MemUsedMB = float64(parsed.MemUsedKB) / 1024
	out.MemFreeMB = float64(parsed.MemFreeKB) / 1024
	out.CpuIdlePct = parsed.CpuIdlePct
	if len(parsed.Procs) > n {
		parsed.Procs = parsed.Procs[:n]
	}
	out.Processes = parsed.Procs
	out.Count = len(out.Processes)
	return nil, out, nil
}

type topParsed struct {
	MemUsedKB  uint64
	MemFreeKB  uint64
	CpuIdlePct float64
	Procs      []procRow
}

// parseTopOutput 解析 busybox top -bn1 输出（首轮即含 %CPU）。
// 列名从表头动态定位，兼容有无 PPID/%MEM 的变体；解析不出进程行时返回 ok=false。
func parseTopOutput(text string) (topParsed, bool) {
	var res topParsed
	var headerCols map[string]int
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Mem:"):
			res.MemUsedKB, res.MemFreeKB = parseTopMemLine(line)
		case strings.HasPrefix(line, "CPU:"):
			res.CpuIdlePct = parseTopCpuLine(line)
		case headerCols == nil && strings.HasPrefix(line, "PID"):
			headerCols = map[string]int{}
			for i, h := range strings.Fields(line) {
				if _, dup := headerCols[h]; !dup {
					headerCols[h] = i
				}
			}
			if _, ok := headerCols["PID"]; !ok {
				headerCols = nil // 表头异常
			}
		case headerCols != nil && line != "":
			if row, ok := parseTopProcRow(line, headerCols); ok {
				res.Procs = append(res.Procs, row)
			}
		}
	}
	if len(res.Procs) == 0 {
		return res, false
	}
	// 按 CPU 降序（busybox 输出本身有序，显式排序保证稳定）
	sort.SliceStable(res.Procs, func(i, j int) bool { return res.Procs[i].CpuPct > res.Procs[j].CpuPct })
	return res, true
}

func parseTopMemLine(line string) (used, free uint64) {
	f := strings.Fields(line)
	for i := 1; i < len(f)-1; i++ {
		v, err := strconv.ParseUint(strings.TrimSuffix(f[i], "K"), 10, 64)
		if err != nil {
			continue
		}
		label := strings.TrimSuffix(f[i+1], ",")
		switch label {
		case "used":
			used = v
		case "free":
			free = v
		}
	}
	return used, free
}

func parseTopCpuLine(line string) float64 {
	f := strings.Fields(line)
	for i := 1; i < len(f); i++ {
		if f[i] == "idle" && i > 0 {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(f[i-1], "%"), 64); err == nil {
				return v
			}
		}
	}
	return 0
}

func parseTopProcRow(line string, cols map[string]int) (procRow, bool) {
	var row procRow
	f := strings.Fields(line)
	if len(f) <= cols["PID"] {
		return row, false
	}
	pid, err := strconv.Atoi(f[cols["PID"]])
	if err != nil {
		return row, false
	}
	row.PID = pid
	if i, ok := cols["USER"]; ok && i < len(f) {
		row.User = f[i]
	}
	if i, ok := cols["STAT"]; ok && i < len(f) {
		row.Stat = f[i]
	}
	if i, ok := cols["VSZ"]; ok && i < len(f) {
		row.VszKB, _ = strconv.ParseUint(f[i], 10, 64)
	}
	if i, ok := cols["%VSZ"]; ok && i < len(f) {
		row.MemPct, _ = strconv.ParseFloat(strings.TrimSuffix(f[i], "%"), 64)
	}
	if i, ok := cols["%CPU"]; ok && i < len(f) {
		row.CpuPct, _ = strconv.ParseFloat(strings.TrimSuffix(f[i], "%"), 64)
	}
	if i, ok := cols["COMMAND"]; ok && i < len(f) {
		row.Command = strings.Join(f[i:], " ")
	}
	if row.Command == "" {
		return row, false
	}
	return row, true
}

// RegisterResources 注册 resources 域工具。
func RegisterResources(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.system_resources",
		Description: "CPU (cores/model/frequency), memory and swap usage, load averages and temperature, read directly from /proc and /sys.",
	}, systemResourcesHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.disk_usage",
		Description: "Disk usage from df: per-filesystem size/used/avail/use%, pseudo filesystems filtered, root (overlay) and /mnt external mounts flagged.",
	}, diskUsageHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.top_processes",
		Description: "Top processes by CPU from busybox top: default 10 (max 20), with memory and CPU idle summary. Falls back to raw truncated output when parsing fails.",
	}, topProcessesHandler)
}
