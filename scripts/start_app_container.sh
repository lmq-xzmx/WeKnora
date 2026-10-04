#!/usr/bin/env bash
# start_app_container.sh — 启动 WeKnora-app 容器并接入正确的 Docker 网络
#
# 背景：docker-compose.yml 里的 weknora-app 服务假定了一个由 compose 创建的
# 网络。但当你重启 Docker Desktop / 手动启停容器时，compose 项目名变化、旧容器
# 还占用 weknora-app 名字、或网络被清理掉——以上任一种情况都会让
# `docker compose up -d app` 失败，并报
#   - "container name '/WeKnora-app' is already in use"
#   - "lookup redis on 127.0.0.11:53: no such host"
#   - "use of closed network connection"
#
# 这个脚本绕过 docker compose，直接 docker run 启动一个干净的 app 容器，
# 并 --network 强制加入 redis/postgres/docreader 所在的同一网桥，DNS 即恢复。
#
# 用法：
#   ./scripts/start_app_container.sh              # 启动
#   ./scripts/start_app_container.sh --recreate   # 先 rm 再 run
#   ./scripts/start_app_container.sh --stop      # 停掉
#
# 假设：
#   - WeKnora-redis / WeKnora-postgres / WeKnora-docreader 容器在跑
#     （用 make dev-start 启动）
#   - .env 文件存在并已被编辑（APP_PORT=8080、SSRF_WHITELIST_EXTRA 含
#     host.docker.internal / 172.17.0.0/16 等）
#   - BGE 代理在宿主机 3042 端口运行（Ollama 后端）

set -euo pipefail

CONTAINER_NAME="WeKnora-app"
IMAGE="wechatopenai/weknora-app:latest"
NETWORK="weknora_WeKnora-network"
HOST_PORT="${APP_PORT:-5016}"
ENV_FILE="${ENV_FILE:-.env}"

# 数据目录（bind mount 让容器里的应用能写到宿主路径）
DATA_ROOT="/Users/xzmx/Downloads/my-project/WeKnora"
DATA_DIR="${DATA_ROOT}/data/files"

action="start"
for arg in "$@"; do
    case "$arg" in
        --stop) action="stop" ;;
        --recreate) action="recreate" ;;
        *) ;;
    esac
done

stop() {
    if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
        docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
        echo "[ok] removed $CONTAINER_NAME"
    else
        echo "[skip] $CONTAINER_NAME not present"
    fi
}

ensure_data_dir() {
    [ -d "$DATA_DIR" ] || mkdir -p "$DATA_DIR"
}

ensure_network() {
    if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
        echo "[err] docker network '$NETWORK' does not exist."
        echo "      Run: make dev-start   (creates WeKnora-redis/postgres/docreader + network)"
        exit 1
    fi
}

start() {
    ensure_data_dir
    ensure_network

    # 把 .env 转成 -e 参数注入容器（只挑 backend 实际读的项，避免明文写 token）
    # 这里采用 docker run 的 -e 直接传；如需 .env 全部，使用 --env-file
    local env_args=()
    while IFS='=' read -r key val; do
        # 跳过注释 / 空行 / 镜像名/CLI 工具变量
        case "$key" in
            ''|\#*|WEKNORA_VERSION|APK_MIRROR_ARG|NPM_REGISTRY|\
NODE_MAX_OLD_SPACE_SIZE|VITE_FRONTEND_COMMIT|WITH_ANYDOC|\
APT_MIRROR|VITE_DEFAULT_LOCALE|FRONTEND_PORT|APP_PORT|APP_HOST|APP_BACKEND_PORT|APP_SCHEME)
                continue ;;
        esac
        env_args+=("-e" "${key}=${val}")
    done < <(grep -E '^[A-Z_][A-Z0-9_]*=' "$ENV_FILE")

    docker run -d \
        --name "$CONTAINER_NAME" \
        --network "$NETWORK" \
        --restart unless-stopped \
        -p "${HOST_PORT}:8080" \
        -e APP_PORT=8080 \
        -e APP_HOST=app \
        -e GIN_MODE=release \
        -e USER_ID="$(id -u)" \
        -e GROUP_ID="$(id -g)" \
        "${env_args[@]}" \
        -v "${DATA_DIR}:${DATA_DIR}" \
        "$IMAGE" >/dev/null

    echo "[ok] started $CONTAINER_NAME (host port ${HOST_PORT} → container 8080)"
    echo "     health: curl -s http://localhost:${HOST_PORT}/health"
}

recreate() {
    stop
    start
}

case "$action" in
    start) start ;;
    recreate) recreate ;;
    stop) stop ;;
esac