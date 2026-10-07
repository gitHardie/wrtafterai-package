// services 域：router.service_status / router.docker_containers。
package tools

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- router.service_status ----

type serviceRow struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
}

type serviceStatusOutput struct {
	Count     int          `json:"count"`
	Shown     int          `json:"shown"`
	Truncated bool         `json:"truncated,omitempty"`
	Services  []serviceRow `json:"services,omitempty"`
	Note      string       `json:"note,omitempty"`
}

type serviceStatusInput struct {
	Service string `json:"service,omitempty" jsonschema:"optional exact service name to filter (e.g. dnsmasq)"`
}

func serviceStatusHandler(ctx context.Context, req *mcp.CallToolRequest, in serviceStatusInput) (*mcp.CallToolResult, serviceStatusOutput, error) {
	var out serviceStatusOutput

	names, enabled, err := scanInitdServices("/etc/init.d", "/etc/rc.d")
	if err != nil {
		return nil, out, &simpleError{"枚举 init.d 服务失败: " + err.Error()}
	}

	running := map[string]bool{}
	var raw json.RawMessage
	if err := backend.UbusCall(ctx, "service", "list", nil, &raw); err != nil {
		out.Note = "ubus service list 不可用，running 状态全部未知: " + briefErr(err)
	} else {
		running = parseServiceListJSON(raw)
	}

	for _, name := range names {
		if in.Service != "" && name != in.Service {
			continue
		}
		out.Services = append(out.Services, serviceRow{Name: name, Enabled: enabled[name], Running: running[name]})
	}
	out.Count = len(out.Services)
	if in.Service != "" && out.Count == 0 {
		out.Note = "未找到服务 " + in.Service
		return nil, out, nil
	}
	if out.Count > 100 {
		out.Truncated = true
		out.Services = out.Services[:100]
		out.Note = joinNote(out.Note, "服务过多，仅显示前 100 个")
	}
	out.Shown = len(out.Services)
	return nil, out, nil
}

// scanInitdServices 枚举 init.d 服务名，并用 /etc/rc.d/S* 符号链接判断开机自启。
func scanInitdServices(initdDir, rcdDir string) ([]string, map[string]bool, error) {
	entries, err := os.ReadDir(initdDir)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	enabled := map[string]bool{}
	if rcdEntries, err := os.ReadDir(rcdDir); err == nil {
		for _, e := range rcdEntries {
			n := e.Name()
			if len(n) < 2 || n[0] != 'S' {
				continue
			}
			i := 1
			for i < len(n) && n[i] >= '0' && n[i] <= '9' {
				i++
			}
			if i < len(n) {
				enabled[n[i:]] = true
			}
		}
	} // /etc/rc.d 缺失时全部视为未启用（不致命）
	return names, enabled, nil
}

// parseServiceListJSON 解析 ubus service list：有该服务且 instances 非空 → running。
func parseServiceListJSON(data []byte) map[string]bool {
	var list map[string]struct {
		Instances map[string]json.RawMessage `json:"instances"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for name, svc := range list {
		if len(svc.Instances) > 0 {
			out[name] = true
		}
	}
	return out
}

// ---- router.docker_containers ----

type dockerContainerRow struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

type dockerContainersOutput struct {
	Installed  bool                 `json:"installed"`
	All        bool                 `json:"all"`
	Count      int                  `json:"count"`
	Containers []dockerContainerRow `json:"containers,omitempty"`
	Note       string               `json:"note,omitempty"`
}

type dockerContainersInput struct {
	All bool `json:"all,omitempty" jsonschema:"include stopped containers (docker ps -a)"`
}

const dockerPSFormat = "{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Image}}"

func dockerContainersHandler(ctx context.Context, req *mcp.CallToolRequest, in dockerContainersInput) (*mcp.CallToolResult, dockerContainersOutput, error) {
	var out dockerContainersOutput
	out.All = in.All

	if findTool("docker") == "" {
		out.Note = "docker 未安装或未启用（可通过 opkg/luci 安装后再使用本工具）"
		return nil, out, nil
	}
	out.Installed = true

	args := []string{"ps", "--format", dockerPSFormat}
	if in.All {
		args = []string{"ps", "-a", "--format", dockerPSFormat}
	}
	text, err := backend.Run(ctx, 15*time.Second, "docker", args...)
	if err != nil {
		return nil, out, &simpleError{"docker 命令执行失败（守护进程可能未运行）: " + briefErr(err)}
	}
	rows := parseDockerPS(text)
	if len(rows) == 0 {
		out.Note = "无容器（或 docker ps 无输出）"
		return nil, out, nil
	}
	if len(rows) > 100 {
		rows = rows[:100]
		out.Note = "容器过多，仅显示前 100 个"
	}
	out.Containers = rows
	out.Count = len(rows)
	return nil, out, nil
}

// parseDockerPS 解析 docker ps --format 的制表符分隔行。
func parseDockerPS(text string) []dockerContainerRow {
	var out []dockerContainerRow
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		out = append(out, dockerContainerRow{Name: f[0], State: f[1], Status: f[2], Image: f[3]})
	}
	return out
}

// RegisterServices 注册 services 域工具。
func RegisterServices(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.service_status",
		Description: "List init.d services with enabled (rc.d S* symlink) and running (ubus service list instances) state; optional exact-name filter. Capped at 100 rows.",
	}, serviceStatusHandler)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "router.docker_containers",
		Description: "List Docker containers (name/state/status/image); default running only, all=true includes stopped. Reports a hint when docker is not installed.",
	}, dockerContainersHandler)
}
