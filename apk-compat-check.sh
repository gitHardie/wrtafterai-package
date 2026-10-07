#!/bin/bash
# ============================================================
# apk-compat-check.sh — WrtAfterAI 插件 apk 兼容性审计（只读）
#
# 面向 ImmortalWrt / OpenWrt 25.12+（apk 原生，opkg 已移除）。
# 用途：CI 门禁 + 本地体检。只报告，不修改文件。
#
# 用法:
#   bash apk-compat-check.sh [扫描目录]      # 默认当前目录
# 退出码:
#   0 = 通过（允许存在 WARN）
#   1 = 存在 ERROR 级问题，必须处理
# ============================================================

set -u

SCAN_ROOT="${1:-.}"
cd "$SCAN_ROOT" 2>/dev/null || { echo "错误: 目录不存在 $SCAN_ROOT"; exit 1; }

ERRORS=0
WARNS=0
REPORT_FILE=$(mktemp)

emit() { # $1=级别 $2=规则 $3=grep 扩展正则 $4=建议
    local level="$1" rule="$2" pattern="$3" advice="$4"
    local hits
    hits=$(grep -rnE \
        --exclude-dir=.git \
        --exclude=apk-compat-check.sh \
        --exclude=apk-compat-fix.sh \
        --exclude='*.po' \
        --exclude='*.mo' \
        --exclude='*.svg' \
        "$pattern" . 2>/dev/null || true)
    [ -z "$hits" ] && return 0
    while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        local f ln content
        f="${hit%%:*}"
        rest="${hit#*:}"
        ln="${rest%%:*}"
        content="${rest#*:}"
        content=$(printf '%s' "$content" | tr -d '\t' | cut -c1-110)
        printf '[%s] %-22s | %s:%s | %s\n           建议: %s\n' "$level" "$rule" "$f" "$ln" "$content" "$advice" >> "$REPORT_FILE"
        if [ "$level" = "ERROR" ]; then
            ERRORS=$((ERRORS + 1))
        else
            WARNS=$((WARNS + 1))
        fi
    done <<< "$hits"
}

echo "=========================================="
echo "  WrtAfterAI apk 兼容性审计"
echo "  扫描目录: $(pwd)"
echo "  时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "=========================================="
echo

# ---------- ERROR 级（必须修复，CI 阻断） ----------
# R1 运行时脚本调用 opkg 命令（25.12 已无 opkg 可执行文件）
emit ERROR "R1 opkg 命令调用" \
    '\bopkg[[:space:]]+(install|remove|update|upgrade|list|info|search|files|export|flag|hold|unhold)\b' \
    'opkg install→apk add；remove→apk del；update→apk upgrade/update；list-installed→apk info；files→apk info -L'

# R2 引用已失效的 opkg 文件路径
emit ERROR "R2 opkg 路径引用" \
    '/etc/opkg|/usr/lib/opkg|/usr/share/opkg|/var/lib/opkg|distfeeds\.conf|customfeeds\.conf' \
    '/etc/opkg/distfeeds.conf→/etc/apk/repositories.d/distfeeds.list；customfeeds→repositories.d/customfeeds.list；/var/lib/opkg、/usr/lib/opkg→/lib/apk/db'

# R3 Makefile 依赖 opkg 或 luci-app-opkg（25.12 均已移除）
emit ERROR "R3 依赖 opkg / luci-app-opkg" \
    '(DEPENDS|LUCI_DEPENDS)[^=]*=.*(\+opkg\b|luci-app-opkg)' \
    '删除该依赖；LuCI 软件包管理页已内置于 luci-mod-system（apk 后端）'

# ---------- WARN 级（建议修复，不阻断） ----------
# R4 自定义打包调用 ipk 时代接口
emit WARN "R4 IPKG/ipkg 打包调用" \
    'IPKG_BUILD|\bipkg[[:space:]]+(install|remove|build)\b' \
    '构建系统按 CONFIG_USE_APK 自动产出 .apk，移除自定义打包调用'

# R5 Makefile 硬编码包格式（构建系统变量，通常无需自行定义）
emit WARN "R5 PKG_EXT 硬编码 ipk" \
    'PKG_EXT[^=]*:=.*ipk' \
    '删除硬编码；包格式由构建系统自动决定'

# R6 版本号含 ~（apk 版本语义敏感，建议 - 或 _）
emit WARN "R6 PKG_VERSION 含 ~" \
    '^PKG_VERSION:=.*~' \
    '将 ~ 替换为 - 或 _（可运行 apk-compat-fix.sh 自动处理）'

# R7 引用 luci-app-opkg（字符串/菜单/依赖）
emit WARN "R7 引用 luci-app-opkg" \
    'luci-app-opkg' \
    '25.12 无此应用；如需软件包管理入口，依赖 luci-mod-system 即可'

# ---------- 汇总 ----------
echo "-------------- 审计明细 ---------------"
if [ -s "$REPORT_FILE" ]; then
    cat "$REPORT_FILE"
else
    echo "（无问题）"
fi
rm -f "$REPORT_FILE"

echo
echo "=========================================="
echo "  审计结果: ERROR=$ERRORS  WARN=$WARNS"
echo "=========================================="

if [ "$ERRORS" -gt 0 ]; then
    echo "❌ 存在 ERROR 级问题，禁止合入；先运行 apk-compat-fix.sh 或人工修复"
    exit 1
fi
echo "✅ 审计通过"
exit 0
