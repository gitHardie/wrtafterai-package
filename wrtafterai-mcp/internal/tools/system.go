// system 域：router.system_info —— 固件信息、uptime、内存、负载、温度。
// 本文件同时是工具实现模式示范：
//  1. 定义 Input/Output struct（json tag + jsonschema 描述）
//  2. handler(ctx, req, *Input) (*mcp.CallToolResult, Output, error)
//  3. Register 里 mcp.AddTool(s, &mcp.Tool{...}, handler)
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type systemInfoInput struct {
	Verbose bool `json:"verbose,omitempty" jsonschema:"include kernel/board details"`
}

type systemInfoOutput struct {
	Hostname   string    `json:"hostname"`
	Model      string    `json:"model"`
	Firmware   string    `json:"firmware"`
	Kernel     string    `json:"kernel,omitempty"`
	UptimeHrs  float64   `json:"uptime_hours"`
	Load       []float64 `json:"load"`
	MemTotalMB uint64    `json:"mem_total_mb"`
	MemFreeMB  uint64    `json:"mem_free_mb"`
	MemUsedPct float64   `json:"mem_used_pct"`
	TempC      []float64 `json:"temperature_c,omitempty"`
}

func systemInfoHandler(ctx context.Context, req *mcp.CallToolRequest, in systemInfoInput) (*mcp.CallToolResult, systemInfoOutput, error) {
	var out systemInfoOutput

	board, err := backend.GetSystemBoard(ctx)
	if err != nil {
		return nil, out, fmt.Errorf("ubus system board: %w", err)
	}
	info, err := backend.GetSystemInfo(ctx)
	if err != nil {
		return nil, out, fmt.Errorf("ubus system info: %w", err)
	}

	out.Hostname = board.Hostname
	out.Model = board.Model
	out.Firmware = board.Release.Description
	if in.Verbose {
		out.Kernel = board.Kernel
	}
	out.UptimeHrs = float64(info.Uptime) / 3600
	out.Load = info.Load
	out.MemTotalMB = info.Memory.Total / 1024
	out.MemFreeMB = (info.Memory.Free + info.Memory.Cached + info.Memory.Buffered) / 1024
	if info.Memory.Total > 0 {
		used := info.Memory.Total - info.Memory.Free - info.Memory.Cached - info.Memory.Buffered
		out.MemUsedPct = float64(used) * 100 / float64(info.Memory.Total)
	}
	out.TempC = backend.Temperature(ctx)
	return nil, out, nil
}

// RegisterSystem 注册 system 域工具。
func RegisterSystem(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.system_info",
		Description: "Router overview: model, firmware, uptime, load, memory usage, temperature. Use this first for any 'how is my router' question.",
	}, systemInfoHandler)
}

// helpers 共用
func truncateLines(s string, max int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[len(lines)-max:], "\n")
}
