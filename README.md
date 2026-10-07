# WrtAfterAI Package

WrtAfterAI 固件的插件仓库（feed），从 [kenzok8/small-package](https://github.com/kenzok8/small-package) 定期同步并做 apk 兼容治理。

> **基座**: ImmortalWrt 25.12+（apk 原生包管理，opkg 已被上游移除）
> **主页**: https://wrt.afterai.tech

## 仓库结构

| 文件 | 作用 |
| --- | --- |
| `SYNC_LIST` | 同步白名单（每行一个包名） |
| `sync-from-upstream.sh` | 本地同步 + 清理 + 自动修复 + 审计 |
| `apk-compat-check.sh` | apk 兼容审计（只读，CI 门禁） |
| `apk-compat-fix.sh` | apk 兼容自动修复（默认 dry-run，`--apply` 写入） |
| `.github/workflows/sync.yml` | 每周自动同步：同步 → 清理 → 修复 → 审计 → 提交 |
| `.github/workflows/build-test.yml` | ImmortalWrt 25.12 逐包编译测试 |

## apk 兼容规则速查

| opkg | apk |
| --- | --- |
| `opkg install` | `apk add` |
| `opkg remove` | `apk del` |
| `opkg update` | `apk update` |
| `/etc/opkg/distfeeds.conf` | `/etc/apk/repositories.d/distfeeds.list` |
| `/etc/opkg/customfeeds.conf` | `/etc/apk/repositories.d/customfeeds.list` |
| `/var/lib/opkg`、`/usr/lib/opkg` | `/lib/apk/db` |
| conffiles | `/etc/apk/protected_paths.d/` |
| `.ipk` | `.apk` |

官方对照表：[opkg-to-apk cheatsheet](https://openwrt.org/docs/guide-user/additional-software/opkg-to-apk-cheatsheet)

## 本地使用

```bash
# 同步并体检（修复为 dry-run 预览）
./sync-from-upstream.sh

# 确认后写入修复
./sync-from-upstream.sh --apply

# 单独审计
bash apk-compat-check.sh
```

## 变更记录

- 2026-10-07：新增自有包 `wrtafterai-mcp` v0.1.0（M0：12 只读工具 + token 管理 + 一键 setup/tunnel/doctor）
- 2026-10-07：移除 `luci-app-xunlei`（闭源 binary 无维护，25.12/apk 下无法可靠适配）
- 2026-10-07：项目由 rclaw 改名 wrtafterai，引入 apk 兼容审计/修复流水线
