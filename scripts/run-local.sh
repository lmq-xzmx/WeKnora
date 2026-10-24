#!/bin/bash
# 本机跑 WeKnora 后端（基础设施仍走 docker：postgres/redis/minio/docreader）。
#
#   ./scripts/run-local.sh            前台跑
#   ./scripts/run-local.sh --bg       后台跑，日志写 /tmp/weknora-local.log
#
# .env 里的连接参数已指向本机映射端口（DB 127.0.0.1:5433、redis :16390、
# minio :9000、docreader :15051），webhook 回调走 127.0.0.1:5002 回 GEO-K。
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

BIN=/tmp/WeKnora-local
if [ ! -x "$BIN" ] || [ "$REPO/internal" -nt "$BIN" ] || [ "$REPO/cmd" -nt "$BIN" ]; then
  echo "[run-local] 编译二进制…"
  CGO_ENABLED=1 go build -o "$BIN" ./cmd/server || exit 1
fi

# 导出 .env 供 viper 的 ${ENV} 替换使用
set -a
# shellcheck disable=SC1091
source "$REPO/.env"
set +a

# 容器里用的这些值在本机语境下要覆盖
export APP_HOST=127.0.0.1
export APP_BACKEND_PORT=5016

LOG=/tmp/weknora-local.log
if [ "${1:-}" = "--bg" ]; then
  echo "[run-local] 后台启动，日志: $LOG"
  nohup "$BIN" >"$LOG" 2>&1 &
  echo "[run-local] PID $!"
  exit 0
fi

echo "[run-local] 前台启动，日志同时写 $LOG"
exec "$BIN" 2>&1 | tee "$LOG"
