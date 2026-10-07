#!/bin/bash
# ============================================================
# apk-compat-fix.sh — apk 兼容性自动修复
#
# 默认 dry-run 只预览；确认无误后加 --apply 实际写入。
# 只做高置信替换；中低置信问题由 apk-compat-check.sh 报告人工处理。
#
# 用法:
#   bash apk-compat-fix.sh            # dry-run 预览
#   bash apk-compat-fix.sh --apply    # 实际修改文件
# ============================================================

set -u

DRY=1
[ "${1:-}" = "--apply" ] && DRY=0
SCAN_ROOT="${2:-.}"
cd "$SCAN_ROOT" 2>/dev/null || { echo "错误: 目录不存在 $SCAN_ROOT"; exit 1; }

TARGETS=()
while IFS= read -r f; do
    TARGETS+=("$f")
done < <(find . -path ./.git -prune -o -type f \
    \( -name "Makefile" -o -name "*.sh" -o -name "*.lua" -o -name "*.htm" \
       -o -name "*.ut" -o -name "*.js" -o -name "*.json" -o -name "*.init" \
       -o -name "*.conf" -o -name "*uci-defaults" \) -print 2>/dev/null)

MODE="DRY-RUN（未写入）"
[ $DRY -eq 0 ] && MODE="已写入"

echo "=========================================="
echo "  apk 兼容自动修复  [$MODE]"
echo "  目标文件数: ${#TARGETS[@]}"
echo "=========================================="
echo

TOTAL=0

fix() { # $1=说明 $2=sed 表达式
    local desc="$1" expr="$2" f n=0
    for f in "${TARGETS[@]}"; do
        if grep -qE "$(echo "$expr" | sed -e 's/^s\///' )" "$f" 2>/dev/null; then :; fi
    done
    for f in "${TARGETS[@]}"; do
        if grep -q "opkg" "$f" 2>/dev/null || grep -qE '~' "$f" 2>/dev/null; then :; fi
    done
    # 逐文件尝试替换并统计实际命中
    for f in "${TARGETS[@]}"; do
        if [ $DRY -eq 1 ]; then
            n=$(sed -e "$expr" "$f" 2>/dev/null | diff - "$f" 2>/dev/null | grep -c '^<' || true)
        else
            before=$(md5sum "$f" 2>/dev/null | cut -d' ' -f1)
            sed -i -e "$expr" "$f" 2>/dev/null
            after=$(md5sum "$f" 2>/dev/null | cut -d' ' -f1)
            [ "$before" != "$after" ] && n=1
        fi
        if [ "${n:-0}" -gt 0 ] 2>/dev/null; then
            echo "  [修复] $desc → $f"
            TOTAL=$((TOTAL + n))
        fi
    done
}

echo "[1/5] opkg 命令 → apk 命令"
fix "opkg install→apk add"      's/\bopkg[[:space:]]\+install\b/apk add/g'
fix "opkg remove→apk del"       's/\bopkg[[:space:]]\+remove\b/apk del/g'
fix "opkg update→apk update"    's/\bopkg[[:space:]]\+update\b/apk update/g'
fix "opkg upgrade→apk upgrade"  's/\bopkg[[:space:]]\+upgrade\b/apk upgrade/g'
echo

echo "[2/5] opkg 源文件路径 → apk 路径"
fix "distfeeds.conf" 's|/etc/opkg/distfeeds\.conf|/etc/apk/repositories.d/distfeeds.list|g'
fix "customfeeds.conf" 's|/etc/opkg/customfeeds\.conf|/etc/apk/repositories.d/customfeeds.list|g'
fix "opkg keys 目录" 's|/etc/opkg/keys|/etc/apk/keys|g'
fix "opkg 目录兜底" 's|/etc/opkg|/etc/apk|g'
echo

echo "[3/5] opkg 数据库路径 → apk db"
fix "/var/lib/opkg→/lib/apk/db" 's|/var/lib/opkg|/lib/apk/db|g'
echo

echo "[4/5] 包格式硬编码"
fix "PKG_EXT:=ipk→apk" 's/\(PKG_EXT[^:=]*:=\s*\)ipk/\1apk/g'
echo

echo "[5/5] PKG_VERSION 版本号 ~ → -（仅 Makefile 的 PKG_VERSION 行）"
for f in $(find . -path ./.git -prune -o -type f -name Makefile -print 2>/dev/null); do
    if grep -q '^PKG_VERSION:=.*~' "$f" 2>/dev/null; then
        if [ $DRY -eq 1 ]; then
            echo "  [待修] 版本号含 ~ → $f"
        else
            sed -i 's|^\(PKG_VERSION:=.*\)~\(.*\)$|\1-\2|' "$f"
            echo "  [修复] 版本号 ~ → - | $f"
        fi
        TOTAL=$((TOTAL + 1))
    fi
done
echo

echo "=========================================="
echo "  完成: 共命中 $TOTAL 处  [$MODE]"
echo "=========================================="
[ $DRY -eq 1 ] && echo "提示: 确认无误后执行  bash apk-compat-fix.sh --apply"
echo "剩余无法自动修复的问题请运行: bash apk-compat-check.sh"
exit 0
