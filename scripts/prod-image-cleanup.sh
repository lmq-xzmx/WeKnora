#!/bin/sh
# 生产机镜像清理
#
# 目标：生产机硬盘留给业务数据，不留无用镜像；同时保证业务在用的镜像
#       始终「有一份保留」且「是新的」。
#
# 流程：
#   1. 汇总「必须保留」的镜像
#        a) compose 文件里声明的镜像（业务依赖）
#        b) 正在运行的容器实际占用的镜像（按 image id 判定）
#   2. 校验：每个必须保留的镜像本地都要有一份；缺的就先拉，
#      拉不到就直接中止 —— 不做任何删除（宁可不清，也不能把业务镜像删没了）
#   3. 删除其余镜像（运行中容器占用的不动）
#   4. 重新拉取必须保留的镜像，保证它们是 registry 里的最新版
#      （已是最新时 docker 只做一次 manifest 校验，开销极小）
#
# 用法：
#   prod-image-cleanup.sh                 # 正常清理
#   DRY_RUN=1 prod-image-cleanup.sh       # 只打印不删
#
# 部署：/usr/local/bin/prod-image-cleanup.sh + crontab（每天一次）
set -e

COMPOSE_DIR=${COMPOSE_DIR:-/data/WeKnora}
DRY_RUN=${DRY_RUN:-0}
LOG_TAG=prod-image-cleanup

log() { echo "[$(date '+%F %T')] $*"; }

# ── 1. 汇总必须保留的镜像 ──────────────────────────────
cd "$COMPOSE_DIR" || { log "找不到 $COMPOSE_DIR，退出"; exit 1; }

REQUIRED_REFS=$(docker-compose config --images 2>/dev/null | sort -u | grep -v '^$' || true)
[ -n "$REQUIRED_REFS" ] || { log "compose 里没解析出镜像，退出（避免误删）"; exit 1; }

# 运行中容器占用的镜像 id（sha256:...），这些一律不动
PROTECTED_IDS=$(docker ps -q | while read -r cid; do
  docker inspect -f '{{.Image}}' "$cid" 2>/dev/null
done | sort -u)

log "必须保留的镜像 ${REQUIRED_REFS}" | tr '\n' ' '
echo ""

# ── 2. 校验：每个必须保留的镜像本地都要有 ──────────────
MISSING=""
for ref in $REQUIRED_REFS; do
  docker image inspect "$ref" >/dev/null 2>&1 || MISSING="$MISSING $ref"
done

if [ -n "$MISSING" ]; then
  log "以下必要镜像本地缺失，先补拉：$MISSING"
  for ref in $MISSING; do
    if ! docker pull "$ref"; then
      log "拉取失败：$ref —— 中止清理（不动任何镜像）"
      exit 1
    fi
  done
fi

# 补拉后必须全部存在，否则说明环境有问题，宁可不清
for ref in $REQUIRED_REFS; do
  if ! docker image inspect "$ref" >/dev/null 2>&1; then
    log "必要镜像仍缺失：$ref —— 中止清理"
    exit 1
  fi
done
log "必要镜像校验通过（$(echo "$REQUIRED_REFS" | wc -l | tr -d ' ') 个）"

# ── 3. 删除其余镜像 ────────────────────────────────────
# 按 image id 归组：只要该 id 上挂着任意一个必要 tag，就整体保留
KEEP_IDS=$(for ref in $REQUIRED_REFS; do
  docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null
done | sort -u)

REMOVED=0
KEEP_MB=0
for id in $(docker images --format '{{.ID}}' | sort -u); do
  full=$(docker image inspect -f '{{.Id}}' "$id" 2>/dev/null)
  [ -n "$full" ] || continue
  case "$full" in
    sha256:*) : ;;
    *) continue ;;
  esac
  if echo "$KEEP_IDS" | grep -qx "$full"; then continue; fi
  if echo "$PROTECTED_IDS" | grep -qx "$full"; then continue; fi
  if [ "$DRY_RUN" = "1" ]; then
    sz=$(docker image inspect -f '{{.Size}}' "$id" 2>/dev/null || echo 0)
    log "[DRY_RUN] 将删除 $id ($(echo "$sz" | awk '{printf "%.1f MB", $1/1048576}'))"
    continue
  fi
  if docker image rm "$id" >/dev/null 2>&1; then
    sz=$(docker image inspect -f '{{.Size}}' "$id" 2>/dev/null || echo 0)
    log "已删除 $id"
    REMOVED=$((REMOVED + 1))
  else
    log "删除失败（可能仍被引用）：$id"
  fi
done

# 悬空镜像（<none>）一并清掉
if [ "$DRY_RUN" != "1" ]; then
  docker image prune -f >/dev/null 2>&1 || true
fi

# ── 4. 刷新保留的镜像，保证是新的 ──────────────────────
if [ "$DRY_RUN" != "1" ]; then
  for ref in $REQUIRED_REFS; do
    if ! docker pull "$ref" >/dev/null 2>&1; then
      log "刷新失败（保留现有版本）：$ref"
    fi
  done
  log "必要镜像已刷新到 registry 最新版"
fi

log "清理完成：删除 $REMOVED 个镜像"
docker system df 2>/dev/null | head -4
