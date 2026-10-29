#!/bin/sh
# 生产机镜像清理
#
# 目标：生产机硬盘留给业务数据，不留无用镜像；同时保证业务在用的镜像
#       始终「有一份保留」且「是新的」。
#
# 流程：
#   1. 汇总「必须保留」的镜像
#        a) compose 文件里声明的镜像（业务依赖，docker-compose v1 / docker compose v2 都支持）
#        b) 正在运行的容器实际占用的镜像（按 image id 判定，一定不动）
#   2. 校验：compose 声明的业务镜像本地都要有一份；缺的就先拉，
#      拉不到就直接中止 —— 不做任何删除（宁可不清，也不能把业务镜像删没了）
#   3. 删除其余镜像（运行中容器占用的不动）
#   4. 重新拉取业务镜像，保证它们是 registry 里的最新版
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

log() { echo "[$(date '+%F %T')] $*"; }

compose_images() {
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose config --images 2>/dev/null || true
  elif docker compose version >/dev/null 2>&1; then
    docker compose config --images 2>/dev/null || true
  fi
}

# ── 1. 汇总必须保留的镜像 ──────────────────────────────
cd "$COMPOSE_DIR" 2>/dev/null || { log "找不到 $COMPOSE_DIR，退出"; exit 1; }

BUSINESS_REFS=$(compose_images | sort -u | grep -v '^$' || true)
RUNNING_REFS=$(docker ps --format '{{.Image}}' | sort -u | grep -v '^$' || true)

# 运行中容器占用的镜像 id（sha256:...），这些一律不动
PROTECTED_IDS=$(docker ps -q | while read -r cid; do
  docker inspect -f '{{.Image}}' "$cid" 2>/dev/null || true
done | sort -u | grep -v '^$' || true)

if [ -z "$BUSINESS_REFS" ]; then
  log "警告：读不到 compose 镜像清单（$COMPOSE_DIR），退化为只保护运行中容器的镜像"
fi
log "业务镜像 ${BUSINESS_REFS:-<无>}"
log "运行中镜像 ${RUNNING_REFS:-<无>}"

# ── 2. 校验：业务镜像本地都要有一份 ────────────────────
MISSING=""
for ref in $BUSINESS_REFS; do
  docker image inspect "$ref" >/dev/null 2>&1 || MISSING="$MISSING $ref"
done

if [ -n "$MISSING" ]; then
  log "以下业务镜像本地缺失，先补拉：$MISSING"
  for ref in $MISSING; do
    if ! docker pull "$ref"; then
      log "拉取失败：$ref —— 中止清理（不动任何镜像）"
      exit 1
    fi
  done
fi

# 补拉后必须全部存在，否则说明环境有问题，宁可不清
for ref in $BUSINESS_REFS; do
  if ! docker image inspect "$ref" >/dev/null 2>&1; then
    log "业务镜像仍缺失：$ref —— 中止清理"
    exit 1
  fi
done
log "业务镜像校验通过（$(echo "$BUSINESS_REFS" | grep -c . || true) 个）"

# ── 3. 删除其余镜像 ────────────────────────────────────
# 按 image id 归组：只要该 id 上挂着任意一个必要 tag，就整体保留
KEEP_IDS=$(for ref in $BUSINESS_REFS $RUNNING_REFS; do
  docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null || true
done | sort -u | grep -v '^$' || true)

REMOVED=0
for id in $(docker images --format '{{.ID}}' | sort -u); do
  full=$(docker image inspect -f '{{.Id}}' "$id" 2>/dev/null || true)
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
    log "已删除 $id"
    REMOVED=$((REMOVED + 1))
  else
    log "删除失败（可能仍被引用）：$id"
  fi
done

# 悬空镜像（<none>）一并清掉
if [ "$DRY_RUN" != "1" ]; then
  docker image prune -f >/dev/null 2>&1 || true
  # 构建缓存：CI 在本机构建时留下的中间层，保留 7 天内的，其余清掉
  # 注意 CI 用的是 buildx，缓存挂在 buildx 上，`docker builder prune` 管不到
  if docker buildx version >/dev/null 2>&1; then
    docker buildx prune -f --filter until=168h >/dev/null 2>&1 || true
  else
    docker builder prune -f --filter until=168h >/dev/null 2>&1 || true
  fi
fi

# ── 4. 刷新业务镜像，保证是新的 ────────────────────────
if [ "$DRY_RUN" != "1" ]; then
  for ref in $BUSINESS_REFS; do
    docker pull "$ref" >/dev/null 2>&1 || log "刷新失败（保留现有版本）：$ref"
  done
  log "业务镜像已刷新到 registry 最新版"
fi

log "清理完成：删除 $REMOVED 个镜像"
docker system df 2>/dev/null | head -4
