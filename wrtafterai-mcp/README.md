# wrtafterai-mcp

跑在 **路由器本体** 上的 MCP（Model Context Protocol）Server：让 Coze / WorkBuddy 等任意 AI Agent 安全访问你的 WrtAfterAI 路由器——查连接数、IP、速度、接口、无线客户端，以及（受控的）配置操作。

## 快速开始（一键）

固件内置后**开箱即用**，首启自动完成：

1. 默认配置写入（`0.0.0.0:8443`，审计开启）
2. 局域网域名解析：`wrt.afterai` → 路由器 LAN IP
3. 生成初始管理 token，保存在 **`/root/wrtafterai-mcp-token.txt`**

```sh
# 查看初始 token
cat /root/wrtafterai-mcp-token.txt

# 体检：确认一切就绪
wrtafterai-mcp doctor
```

## 接入 AI Agent

以 Coze / WorkBuddy 的 MCP 配置为例：

- URL：`http://wrt.afterai:8443/mcp`（局域网）或隧道地址（见下）
- 认证：`Authorization: Bearer <你的token>`
- 单 Agent 用 readonly token 更安全；管理类 Skill 用 write token

## Token 管理

```sh
wrtafterai-mcp token list                  # 查看所有 token（密钥打码）
wrtafterai-mcp token add alice readonly    # 给家人/演示 agent 发只读 token
wrtafterai-mcp token add admin write       # 管理级 token
wrtafterai-mcp token revoke alice          # 吊销
```

- **readonly**：全部查询工具（接口/WAN/无线/客户端/连接数/流量/DHCP/防火墙/日志）
- **write**：另含受控写操作（uci 白名单 + 两步确认，M1 提供）
- 连续 5 次认证失败自动锁定 15 分钟；所有调用进审计日志

## 远程访问（Cloudflare Tunnel）

```sh
# 方式一：临时域名（trycloudflare.com，无需 CF 账号，重启失效）
wrtafterai-mcp tunnel quick

# 方式二：固定隧道（需 Cloudflare Zero Trust 账号，token 持久化）
wrtafterai-mcp tunnel set <CF_TUNNEL_TOKEN>
```

隧道启用后，Agent 用 `https://<你的隧道域名>/mcp` 从任何网络访问家里的路由器。

## 一键向导

```sh
wrtafterai-mcp setup --domain wrt.afterai
```

交互式完成：server 段检查 → 域名解析 → 初始 token → 接入卡片打印。

## 工具清单（M0 已实现 25 个，全部只读）

**核心网络与设备**

| 工具 | 说明 |
| --- | --- |
| `router.system_info` | 主机/固件/内核/负载/内存/温度 |
| `router.network_interfaces` | 接口与 IP/网关/DNS/收发字节 |
| `router.wan_status` | WAN/IPv6 状态 |
| `router.wireless_clients` | 无线客户端（信号/速率） |
| `router.connected_devices` | 在线设备（DHCP+ARP 合并） |
| `router.connection_stats` | 连接跟踪统计（TCP/UDP/ICMP） |
| `router.realtime_traffic` | 实时接口速率 |
| `router.traffic_by_device` | 分设备流量（需 nlbwmon） |
| `router.dhcp_leases` | DHCP 租约 |
| `router.firewall_rules` | 防火墙摘要 |
| `router.system_log` | 最近系统日志 |
| `router.speed_test` | 测速（需 speedtest-cli，固件可选） |

**资源/日志/DNS/诊断/服务**

| 工具 | 说明 |
| --- | --- |
| `router.system_resources` | CPU 核数/频率、swap、1/5/15 负载、温度 |
| `router.disk_usage` | 分区用量、overlay 余量、外置盘 |
| `router.top_processes` | 进程 CPU/内存占用排行 |
| `router.kernel_log` | 内核日志（dmesg） |
| `router.public_ip` | WAN IP + 出口公网 IP（NAT/CGNAT 判断） |
| `router.dns_resolve` | DNS 解析记录查询（可指定上游） |
| `router.dns_config` | 当前上游 DNS 与自定义解析条目 |
| `router.routes` | IPv4/IPv6 路由表 |
| `router.ping` | 连通性诊断（丢包率/延迟） |
| `router.service_status` | 服务启用/运行状态 |
| `router.docker_containers` | Docker 容器状态（未安装优雅提示） |
| `router.get_config` | uci 配置只读查询（敏感字段脱敏） |
| `router.wireless_config` | WiFi 配置（SSID/信道/加密，密码脱敏） |

规划中（批3 评估）：周边信道扫描、VPN/代理状态、外置盘 SMART 健康、apk 可升级包列表。M1 受控写：uci 白名单两步确认、按 MAC 断网、端口转发。

## 传输模式

- **HTTP（默认）**：`wrtafterai-mcp serve --http`，Streamable HTTP + Bearer 认证，供局域网/隧道接入
- **stdio**：`wrtafterai-mcp serve --stdio`，本机 CLI Agent 场景

## 开发

```sh
go build ./... && go vet ./... && go test ./...
```

架构与安全模型见主仓库 `wrtafterai-mcp插件设计_v1.md`。
