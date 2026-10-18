#!/bin/bash
# ==============================================================================
# WeKnora 生产环境部署脚本（支持 SSH 断开后继续）
# ==============================================================================
set -e

REGISTRY="100.108.142.49:5000"
VERSION="${WEKNORA_VERSION:-latest}"
LOG_FILE="/data/weknora-deploy.log"
PID_FILE="/data/weknora-deploy.pid"
STATUS_FILE="/data/weknora-deploy.status"

# 颜色
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

log()  { echo -e "$(date '+%Y-%m-%d %H:%M:%S') $1"; }
ok()   { log "${GREEN}[OK]${NC} $1"; }
warn() { log "${YELLOW}[WARN]${NC} $1"; }
err()  { log "${RED}[ERR]${NC} $1"; }

# ------------------------------------------------------------------------------
# 检查前置条件
# ------------------------------------------------------------------------------
check() {
    log "检查前置条件..."

    # 检查 Docker daemon
    if ! docker info >/dev/null 2>&1; then
        err "Docker 未运行，请先启动 Docker"
        exit 1
    fi

    # 检查 Docker network
    if ! docker network ls | grep -q "weknora_weknora-network"; then
        log "创建 Docker 网络..."
        docker network create weknora_weknora-network 2>/dev/null || true
    fi

    # 检查 .env 文件
    if [ ! -f "/data/WeKnora/.env" ]; then
        if [ -f "/data/WeKnora/.env.example" ]; then
            warn ".env 不存在，从 .env.example 复制（请编辑 /data/WeKnora/.env 配置必填项）"
            cp /data/WeKnora/.env.example /data/WeKnora/.env
        fi
    fi

    # ===== 安全检查 =====
    # 加载 .env 文件（如果存在）
    set -a
    [ -f "/data/WeKnora/.env" ] && source "/data/WeKnora/.env"
    set +a

    # 检查 Neo4j 默认密码
    if [ "${NEO4J_PASSWORD:-password}" = "password" ]; then
        warn "Neo4j 使用默认密码 'password'，生产环境必须修改 NEO4J_PASSWORD"
    fi

    # 检查 MinIO 默认凭证
    if [ "${MINIO_SECRET_ACCESS_KEY:-minioadmin}" = "minioadmin" ]; then
        warn "MinIO 使用默认密码 'minioadmin'，生产环境必须修改 MINIO_SECRET_ACCESS_KEY"
    fi

    # 检查 JWT_SECRET 未设置
    if [ -z "${JWT_SECRET}" ]; then
        warn "JWT_SECRET 未设置，生产环境必须设置强随机值（建议 32+ 字符）"
    fi

    # 检查 SYSTEM_AES_KEY 未设置（敏感字段加密密钥）
    if [ -z "${SYSTEM_AES_KEY}" ]; then
        warn "SYSTEM_AES_KEY 未设置，生产环境建议设置 32 字节 AES-256 密钥以加密敏感数据"
    fi

    # 检查 Langfuse 默认凭证（如果启用 langfuse profile）
    if [ "${LANGFUSE_MINIO_PASSWORD:-langfuseminiosecret}" = "langfuseminiosecret" ]; then
        warn "Langfuse MinIO 使用默认密码，生产环境必须修改 LANGFUSE_MINIO_PASSWORD"
    fi

    ok "前置检查完成"
}

# ------------------------------------------------------------------------------
# 拉取镜像
# ------------------------------------------------------------------------------
pull_images() {
    log "从 ${REGISTRY} 拉取镜像（版本: ${VERSION}）..."

    IMAGES=(
        "wechatopenai/weknora-app:${VERSION}"
        "wechatopenai/weknora-ui:${VERSION}"
        "wechatopenai/weknora-docreader:${VERSION}"
        "paradedb/paradedb:v0.22.6-pg17"
        "redis:7.0-alpine"
        "searxng/searxng:latest"
        "minio/minio:RELEASE.2025-09-07T16-13-09Z"
        "neo4j:2025.10.1"
        "qdrant/qdrant:v1.16.2"
    )

    for img in "${IMAGES[@]}"; do
        full_img="${REGISTRY}/${img}"
        echo -n "  拉取 ${full_img} ... "
        docker pull "${full_img}" >> "${LOG_FILE}" 2>&1 && RUN_OK=$? || RUN_OK=$?
        if [ "${RUN_OK}" -eq 0 ]; then
            echo -e "${GREEN}成功${NC}"
        else
            echo -e "${RED}失败${NC}"
            # 尝试不带 registry 前缀拉取（公共镜像）
            echo -n "  尝试拉取公共镜像 ${img} ... "
            docker pull "${img}" >> "${LOG_FILE}" 2>&1 && RUN_OK=$? || RUN_OK=$?
            if [ "${RUN_OK}" -eq 0 ]; then
                echo -e "${GREEN}成功${NC}"
            else
                echo -e "${RED}失败${NC}"
                warn "镜像 ${img} 拉取失败，继续其他任务"
            fi
        fi
    done
    ok "镜像拉取完成"
}

# ------------------------------------------------------------------------------
# 启动服务
# ------------------------------------------------------------------------------
start_services() {
    log "启动 WeKnora 服务..."
    cd /data/WeKnora

    # 停止旧容器（忽略错误）
    docker compose down 2>/dev/null || true

    # 启动核心服务（不加 full profile，只启动基础服务）
    docker compose up -d postgres redis app frontend

    ok "服务启动完成"
}

# ------------------------------------------------------------------------------
# 查看状态
# ------------------------------------------------------------------------------
status() {
    cd /data/WeKnora
    echo ""
    echo "========== 容器状态 =========="
    docker compose ps
    echo ""
    echo "========== 健康检查 =========="
    for container in WeKnora-app WeKnora-frontend WeKnora-postgres WeKnora-redis; do
        if docker ps --format '{{.Names}}' | grep -q "^${container}$"; then
            health=$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null || echo "无健康检查")
            status=$(docker inspect --format='{{.State.Status}}' "${container}" 2>/dev/null || echo "未知")
            echo "  ${container}: ${status} | 健康=${health}"
        else
            echo "  ${container}: 未运行"
        fi
    done
    echo ""
    echo "========== 最近日志 =========="
    docker compose logs --tail=20
}

# ------------------------------------------------------------------------------
# 查看日志
# ------------------------------------------------------------------------------
logs() {
    cd /data/WeKnora
    docker compose logs -f "$@"
}

# ------------------------------------------------------------------------------
# 完整部署流程
# ------------------------------------------------------------------------------
deploy() {
    check
    pull_images
    start_services

    log "等待服务健康检查..."
    sleep 10
    status

    ok "部署完成！"
    log "访问地址: http://$(hostname -I | awk '{print $1}'):${FRONTEND_PORT:-80}"
}

# ------------------------------------------------------------------------------
# 入口
# ------------------------------------------------------------------------------
CMD="${1:-deploy}"

case "$CMD" in
    deploy)       deploy ;;
    pull)          check; pull_images ;;
    start)         start_services ;;
    stop)          cd /data/WeKnora && docker compose down ;;
    restart)       cd /data/WeKnora && docker compose restart ;;
    status)        status ;;
    logs)          shift; logs "$@" ;;
    check)         check ;;
    *)             echo "用法: $0 {deploy|pull|start|stop|restart|status|logs|check}"
                   echo "  deploy  - 完整部署（默认）"
                   echo "  pull    - 仅拉取镜像"
                   echo "  start   - 仅启动服务"
                   echo "  stop    - 停止服务"
                   echo "  restart - 重启服务"
                   echo "  status  - 查看状态"
                   echo "  logs    - 查看日志（可加 docker compose logs 参数）"
                   echo "  check   - 检查前置条件"
                   exit 1 ;;
esac
