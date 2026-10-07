#!/bin/bash
# ============================================================
# sync-from-upstream.sh — 本地从 kenzok8/small-package 同步插件
# 同步后自动执行: 清理已移除包 → apk 自动修复(dry-run) → 兼容审计
# 用法: ./sync-from-upstream.sh [--apply]
#   --apply: 修复步骤直接写入（默认 dry-run 预览）
# ============================================================

set -e

UPSTREAM_REPO="https://github.com/kenzok8/small-package.git"
UPSTREAM_BRANCH="main"
SYNC_LIST="SYNC_LIST"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMP_DIR=$(mktemp -d)
FIX_MODE=""
[ "${1:-}" = "--apply" ] && FIX_MODE="--apply"

cleanup() { rm -rf "$TEMP_DIR"; }
trap cleanup EXIT

cd "$SCRIPT_DIR"

echo "=========================================="
echo "  WrtAfterAI Package 同步脚本"
echo "=========================================="
echo

echo "[1/5] 克隆上游仓库..."
git clone --depth 1 -b "$UPSTREAM_BRANCH" "$UPSTREAM_REPO" "$TEMP_DIR/upstream"

echo "[2/5] 同步插件..."
PACKAGES=$(grep -v '^#' "$SYNC_LIST" | grep -v '^$' | tr '\n' ' ')
echo "  同步列表: $PACKAGES"
for pkg in $PACKAGES; do
    src="$TEMP_DIR/upstream/$pkg"
    dst="$SCRIPT_DIR/$pkg"
    if [ -d "$src" ]; then
        echo "  同步: $pkg"
        rm -rf "$dst"
        cp -r "$src" "$dst"
    else
        echo "  跳过: $pkg (上游不存在)"
    fi
done

echo "[3/5] 清理已移除的包..."
for d in "$SCRIPT_DIR"/*/; do
    pkg=$(basename "$d")
    if ! grep -qx "$pkg" "$SYNC_LIST"; then
        echo "  删除: $pkg (不在 SYNC_LIST)"
        rm -rf "$d"
    fi
done

echo "[4/5] apk 自动修复 $([ -z "$FIX_MODE" ] && echo '(dry-run)')..."
bash "$SCRIPT_DIR/apk-compat-fix.sh" $FIX_MODE

echo "[5/5] 兼容审计..."
if bash "$SCRIPT_DIR/apk-compat-check.sh"; then
    echo ""
    echo "=========================================="
    echo "  ✅ 同步完成，审计通过"
    echo "=========================================="
    echo "下一步:"
    echo "  git add ."
    echo "  git commit -m 'Sync from upstream $(date +%Y-%m-%d)'"
    echo "  git push"
else
    echo ""
    echo "⚠️  审计未通过，请按上方报告处理后再提交"
    exit 1
fi
