// speedtest 域：router.speed_test —— 检测并执行 speedtest-cli；iperf3 仅提示。
package tools

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type speedTestOutput struct {
	Tool         string  `json:"tool,omitempty"` // speedtest-cli / iperf3 / 空
	PingMs       float64 `json:"ping_ms,omitempty"`
	DownloadMbps float64 `json:"download_mbps,omitempty"`
	UploadMbps   float64 `json:"upload_mbps,omitempty"`
	Raw          string  `json:"raw,omitempty"`
	Note         string  `json:"note,omitempty"`
}

type speedTestInput struct{}

// speedTestHandler 超时 60s；M0 不内置任何测速工具的安装逻辑。
func speedTestHandler(ctx context.Context, req *mcp.CallToolRequest, in speedTestInput) (*mcp.CallToolResult, speedTestOutput, error) {
	var out speedTestOutput

	stCLI, _ := exec.LookPath("speedtest-cli")
	iperf, _ := exec.LookPath("iperf3")

	switch {
	case stCLI != "":
		out.Tool = "speedtest-cli"
		raw, err := backend.Run(ctx, 60*time.Second, "speedtest-cli", "--simple")
		if err != nil {
			return nil, out, &simpleError{"speedtest-cli 执行失败（外网测速可能被墙或超时）: " + briefErr(err)}
		}
		out.Raw = strings.TrimSpace(raw)
		for _, line := range strings.Split(out.Raw, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				continue
			}
			switch strings.ToLower(strings.TrimSuffix(f[0], ":")) {
			case "ping":
				out.PingMs = v
			case "download":
				out.DownloadMbps = v
			case "upload":
				out.UploadMbps = v
			}
		}
	case iperf != "":
		out.Tool = "iperf3"
		out.Note = "检测到 iperf3，但测速需要指定服务端（iperf3 -c <server>）。M0 不内置 iperf3 服务端联动，建议安装 speedtest-cli 或在固件集成。"
	default:
		out.Note = "测速工具未安装，M0 不内置，建议安装 speedtest-cli 或在固件集成。"
	}
	return nil, out, nil
}

// RegisterSpeedtest 注册 speedtest 域工具。
func RegisterSpeedtest(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.speed_test",
		Description: "Run internet speed test via speedtest-cli if installed (ping/download/upload). Falls back to guidance when no tool is available. Takes up to 60s.",
	}, speedTestHandler)
}
