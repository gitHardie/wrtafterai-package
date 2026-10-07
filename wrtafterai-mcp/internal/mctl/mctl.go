// Package mctl：CLI 管理命令（token / setup / tunnel / doctor）。
// 所有写操作通过 exec uci（参数数组），不进 shell。
package mctl

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gitHardie/wrtafterai-mcp/internal/backend"
	"github.com/gitHardie/wrtafterai-mcp/internal/policy"
)

const uciPkg = "wrtafterai-mcp"

func uci(args ...string) (string, error) {
	b, err := exec.Command("uci", args...).CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func uciOK(args ...string) error {
	_, err := uci(args...)
	return err
}

func randKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- token ----

// TokenCmd 处理 token add/list/revoke。
func TokenCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wrtafterai-mcp token add <name> <level> | list | revoke <name>")
	}
	switch args[0] {
	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: token add <name> <level=readonly|write>")
		}
		name, lvl := args[1], policy.ParseLevel(args[2])
		if name == "server" {
			return fmt.Errorf("reserved name")
		}
		key := randKey()
		if err := uciOK("set", fmt.Sprintf("%s.%s=token", uciPkg, name)); err != nil {
			return fmt.Errorf("uci set (uci 可用? 是则在路由器上执行): %w", err)
		}
		if err := uciOK("set", fmt.Sprintf("%s.%s.key='%s'", uciPkg, name, key)); err != nil {
			return err
		}
		if err := uciOK("set", fmt.Sprintf("%s.%s.level='%s'", uciPkg, name, lvl)); err != nil {
			return err
		}
		if err := uciOK("commit", uciPkg); err != nil {
			return err
		}
		fmt.Printf("token created\n  name : %s\n  level: %s\n  key  : %s\n", name, lvl, key)
		fmt.Println("把 key 填入 Agent 平台 MCP 配置的 Authorization: Bearer <key>（Coze: Service token/Header）")
		return nil
	case "list":
		out, err := uci("-q", "show", uciPkg)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	case "revoke":
		if len(args) < 2 {
			return fmt.Errorf("usage: token revoke <name>")
		}
		if err := uciOK("delete", fmt.Sprintf("%s.%s", uciPkg, args[1])); err != nil {
			return err
		}
		return uciOK("commit", uciPkg)
	default:
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
}

// ---- setup 一键初始化 ----

// SetupCmd：确保 server 段、生成首个 token（可 --name/--level 覆盖）、写 dnsmasq 域名解析、打印接入卡片。
func SetupCmd(args []string, version string) error {
	domain := "wrt.afterai"
	port := "8443"
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--domain":
			domain = args[i+1]
		case "--port":
			port = args[i+1]
		}
	}
	ctx := context.Background()

	// 1. server 基础段
	if out, _ := uci("-q", "get", uciPkg+".server.listen"); out == "" {
		if err := uciOK("set", uciPkg+".server=server"); err != nil {
			return fmt.Errorf("无法写 uci（请在路由器上执行）: %w", err)
		}
		uciOK("set", uciPkg+".server.listen='0.0.0.0:"+port+"'")
		uciOK("set", uciPkg+".server.domain='"+domain+"'")
		uciOK("commit", uciPkg)
	}

	// 2. 首个 token（缺省 admin/readonly）
	existing, _ := uci("-q", "show", uciPkg)
	if !strings.Contains(existing, "=token") {
		fmt.Println("→ 生成首个接入 token：")
		if err := TokenCmd([]string{"add", "admin", "readonly"}); err != nil {
			return err
		}
	}

	// 3. dnsmasq 局域网域名解析 wrt.afterai -> LAN IP
	lanIP := ""
	var lanStatus struct {
		IPv4Address []struct {
			Address string `json:"address"`
		} `json:"ipv4-address"`
	}
	if err := backend.UbusCall(ctx, "network.interface.lan", "status", nil, &lanStatus); err == nil && len(lanStatus.IPv4Address) > 0 {
		lanIP = lanStatus.IPv4Address[0].Address
	}
	if lanIP != "" {
		target := fmt.Sprintf("/%s/%s", domain, lanIP)
		if out, _ := uci("-q", "get", "dhcp.@dnsmasq[0]"); out != "" {
			uciOK("add_list", "dhcp.@dnsmasq[0].address='"+target+"'")
			uciOK("commit", "dhcp")
			_ = exec.Command("/etc/init.d/dnsmasq", "restart").Run()
			fmt.Printf("→ 已写入局域网解析: %s -> %s（dnsmasq 已重启）\n", domain, lanIP)
		}
	} else {
		fmt.Println("→ 跳过 dnsmasq 解析（LAN IP 获取失败，可手动: uci add_list dhcp.@dnsmasq[0].address='/wrt.afterai/192.168.31.100'）")
	}

	// 4. 接入卡片
	fmt.Printf(`
============================================================
 wrtafterai-mcp %s setup 完成
============================================================
 MCP 端点(LAN)   : http://%s:%s/mcp
 LuCI/浏览器     : http://%s
 审计日志        : /tmp/wrtafterai-mcp/audit.jsonl

 接入 Coze（局域网内/自建）:
   1) 资源库 → 新建插件 → 基于 MCP
   2) MCP 服务地址: %s
   3) 授权: Service token → Header → Authorization = <上面生成的 key>
 接入 WorkBuddy:
   mcp.json → {"mcpServers":{"wrtafterai":{"type":"http","url":"%s","headers":{"Authorization":"Bearer <key>"}}}}

 远程访问(公网): wrtafterai-mcp tunnel set <CF_TOKEN>  （或先 tunnel quick 试用）
 自检: wrtafterai-mcp doctor
============================================================
`, version, lanIP, port, lanIP, "http://"+domain+":"+port+"/mcp", "http://"+domain+":"+port+"/mcp")
	return nil
}

// ---- tunnel ----

// TunnelCmd quick | set <CF_TOKEN>
func TunnelCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tunnel quick | tunnel set <CF_TOKEN>")
	}
	switch args[0] {
	case "quick":
		if _, err := exec.LookPath("cloudflared"); err != nil {
			return fmt.Errorf("cloudflared 未安装（apk add cloudflared）")
		}
		fmt.Println("启动临时隧道（trycloudflare.com，重启失效，仅试用）...")
		cmd := exec.Command("cloudflared", "tunnel", "--url", "http://127.0.0.1:8443", "--no-autoupdate")
		stderr, _ := cmd.StderrPipe()
		cmd.Start()
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, "trycloudflare.com") {
				if u, _, ok := strings.Cut(line, "https://"); ok {
					url := "https://" + strings.TrimSpace(strings.Split(u, " ")[0])
					fmt.Printf("✅ 临时隧道: %s/mcp  （把它填入 Agent 平台，重启后失效）\n", url)
					return nil
				}
			}
		}
		return cmd.Wait()
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("usage: tunnel set <CF_TOKEN>")
		}
		if _, err := exec.LookPath("cloudflared"); err != nil {
			return fmt.Errorf("cloudflared 未安装（apk add cloudflared）")
		}
		if err := uciOK("set", "cloudflared.global=cloudflared"); err != nil {
			return fmt.Errorf("写 cloudflared uci 失败: %w", err)
		}
		uciOK("set", "cloudflared.global.token='"+args[1]+"'")
		uciOK("commit", "cloudflared")
		for _, a := range [][]string{{"enable", "cloudflared"}, {"restart", "cloudflared"}} {
			_ = exec.Command("/etc/init.d/cloudflared", a...).Run()
		}
		fmt.Println("✅ 命名隧道已启用（cloudflared），请在 Cloudflare Zero Trust 中把域名指到 http://localhost:8443")
		return nil
	default:
		return fmt.Errorf("unknown tunnel subcommand %q", args[0])
	}
}

// ---- doctor 自检 ----

// DoctorCmd 输出各检查项状态。
func DoctorCmd(version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fmt.Printf("wrtafterai-mcp doctor (%s)\n", version)
	check("uci 配置可读", func() error { _, err := uci("-q", "show", uciPkg); return err })
	check("ubus system board", func() error { _, err := backend.GetSystemBoard(ctx); return err })
	check("/proc/net/nf_conntrack", func() error { _, err := backend.GetConntrackStats(); return err })
	check("/tmp/dhcp.leases", func() error { _, err := backend.DHCPLeases(); return err })
	check("审计文件", func() error { _, err := os.Stat("/tmp/wrtafterai-mcp/audit.jsonl"); return err })
	out, _ := uci("-q", "show", uciPkg)
	n := strings.Count(out, "=token")
	fmt.Printf("  [info] 已配置 token: %d 个\n", n)
	if n == 0 {
		fmt.Println("  [hint] 尚无 token，运行: wrtafterai-mcp setup")
	}
	return nil
}

func check(name string, fn func() error) {
	status, why := "OK", ""
	if err := fn(); err != nil {
		status, why = "FAIL", err.Error()
	}
	line := fmt.Sprintf("  [%s] %s", status, name)
	if why != "" {
		line += " — " + why
	}
	fmt.Println(line)
}
