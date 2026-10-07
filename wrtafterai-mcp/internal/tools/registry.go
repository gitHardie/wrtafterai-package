// Package tools：MCP 工具集。每个域文件提供 Register<Something>(s *mcp.Server)。
// 权限模型：readonly 工具所有 token 可用；write 工具仅 write token（server 层拦截）。
package tools

import (
	"github.com/gitHardie/wrtafterai-mcp/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps 工具共享依赖。
type Deps struct {
	Audit *audit.Logger
}

// RegisterAll 注册全部工具域（新增域在此追加一行）。
// M0 只读域：system / network / devices / traffic / logs / speedtest。
// 批2 只读域：resources / diagnostics / services / configview。
func RegisterAll(s *mcp.Server, d *Deps) {
	_ = d // write 域工具注册时将使用 audit
	RegisterSystem(s)
	RegisterNetwork(s)
	RegisterDevices(s)
	RegisterTraffic(s)
	RegisterLogs(s)
	RegisterSpeedtest(s)
	RegisterResources(s)
	RegisterDiagnostics(s)
	RegisterServices(s)
	RegisterConfigView(s)
}
