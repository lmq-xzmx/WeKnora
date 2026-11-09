#!/bin/bash
# 本机跑 WeKnora 后端（基础设施仍走 docker：postgres/redis/minio/docreader）。
#
#   ./scripts/run-local.sh                前台跑（默认 profile）
#   ./scripts/run-local.sh --bg           后台跑，日志写 /tmp/weknora-local.log
#   ./scripts/run-local.sh --profile edu  指定场景配置（见 deploy/profiles/）
#
# ── 场景（profile）机制 ──────────────────────────────────────────────
# 为什么需要：一套 WeKnora 代码要服务不同客户/场景（企业信息化 / 教育…），
# 差异全在**配置**里 —— 登录方式（哪个 Casdoor）、品牌、租户策略。
# 有了 profile，切场景 = 换一个 .env 文件，**不改代码、不切 git 分支**。
#
#   deploy/profiles/<name>.env   场景配置（env 覆盖，值最优先）
#   deploy/profiles/README.md     各 profile 的含义与切换说明
#
# 加载顺序（后者覆盖前者）：
#   .env  →  deploy/profiles/<profile>.env  →  命令行临时 export
# 不指定 --profile 时行为与以前完全一致（只读 .env）。
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# ── 参数解析 ────────────────────────────────────────────────────────
PROFILE=""
BG=0
while [ $# -gt 0 ]; do
  case "$1" in
    --profile) PROFILE="${2:-}"; shift 2 ;;
    --profile=*) PROFILE="${1#*=}"; shift ;;
    --bg) BG=1; shift ;;
    *) echo "未知参数: $1"; echo "用法: $0 [--bg] [--profile <名字>]"; exit 2 ;;
  esac
done

BIN=/tmp/WeKnora-local
# 重编译判定：看**具体文件**的时间戳，不是目录 ——
# 目录的 mtime 只在增删文件时变，改目录里的 .go 文件不会更新它。
# 踩过（2026-11-09）：改了 internal/config/config.go 后目录 mtime 没变 →
#   旧二进制一直被复用 → 新功能「配了不生效」，排查了很久。
NEED_BUILD=0
if [ ! -x "$BIN" ]; then
  NEED_BUILD=1
else
  for f in "$REPO/cmd/server/main.go" "$REPO/internal/config/config.go" \
           "$REPO/internal/handler/auth.go" "$REPO/internal/application/service/user.go"; do
    [ -f "$f" ] && [ "$f" -nt "$BIN" ] && NEED_BUILD=1 && break
  done
fi
if [ "$NEED_BUILD" = "1" ]; then
  echo "[run-local] 编译二进制…"
  CGO_ENABLED=1 go build -o "$BIN" ./cmd/server || exit 1
fi

# 导出 .env 供 viper 的 ${ENV} 替换使用
set -a
# shellcheck disable=SC1091
source "$REPO/.env"
set +a

# 场景配置覆盖（可选）
if [ -n "$PROFILE" ]; then
  PROFILE_FILE="$REPO/deploy/profiles/${PROFILE}.env"
  if [ ! -f "$PROFILE_FILE" ]; then
    echo "[run-local] 场景配置不存在: $PROFILE_FILE"
    echo "[run-local] 可用场景: $(ls "$REPO/deploy/profiles" 2>/dev/null | grep '\.env$' | sed 's/\.env$//' | tr '\n' ' ')"
    exit 2
  fi
  echo "[run-local] 加载场景配置: ${PROFILE}.env"
  set -a
  # shellcheck disable=SC1091
  source "$PROFILE_FILE"
  set +a
fi

# 容器里用的这些值在本机语境下要覆盖
export APP_HOST=127.0.0.1
export APP_BACKEND_PORT=5016

LOG=/tmp/weknora-local.log
if [ "$BG" = "1" ]; then
  echo "[run-local] 后台启动，日志: $LOG"
  nohup "$BIN" >"$LOG" 2>&1 &
  echo "[run-local] PID $!"
  exit 0
fi

echo "[run-local] 前台启动，日志同时写 $LOG"
exec "$BIN" 2>&1 | tee "$LOG"
