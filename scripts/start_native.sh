#!/usr/bin/env bash
# start_native.sh — 纯原生模式启动 小炬人（不依赖 Docker Desktop）
#
# 适用：开发机 + 手机流量/VPN + Docker daemon 不稳定的场景。
# 与生产（Docker Compose / K8s）的区别仅在进程边界：
#  - 数据库用本机 brew install 的 PostgreSQL 16（默认 5432）
#  - Redis 用本机 brew install 的 Redis（默认 6379）
#  - DocReader 用本机 Python 进程（端口 50051）
#  - 后端用 make dev-app（go run ./cmd/server，端口 8080）
#  - BGE 代理已常驻 3042，PaddleOCR-VL 已常驻 8085
#
# 用法：
#   ./scripts/start_native.sh           # 启动所有 + 健康检查 + 自动注册一个测试账号
#   ./scripts/start_native.sh --stop   # 停掉所有原生进程
#
# 依赖：brew services 已启动 postgresql@16 和 redis
#       pip3 install paddleocr fastapi uvicorn httpx
#       ollama pull nomic-embed-text
#       数据库迁移已执行（脚本首次运行时会自动跑）

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"
PYTHON="$(command -v python3.12 || command -v python3 || command -v python)"

# Ports
PG_PORT="${PG_PORT:-5432}"
REDIS_PORT="${REDIS_PORT:-6379}"
DOCREADER_PORT="${DOCREADER_PORT:-50051}"
APP_PORT="${APP_PORT:-8080}"
PROXY_PORT="${PROXY_PORT:-3042}"
PADDLEOCR_PORT="${PADDLEOCR_PORT:-8085}"
FRONTEND_PORT="${FRONTEND_PORT:-5015}"

PG_USER="${PG_USER:-postgres}"
PG_PASSWORD="${PG_PASSWORD:-postgres123!@#}"
PG_DB="${PG_DB:-小炬人}"
REDIS_PASSWORD="${REDIS_PASSWORD:-redis123!@#}"

DATA_ROOT="$PROJECT_ROOT"
DATA_DIR="$DATA_ROOT/data/files"

LOG_DIR="$DATA_ROOT/.local-data"

mkdir -p "$DATA_DIR" "$LOG_DIR"

action="start"
for arg in "$@"; do
    case "$arg" in
        --stop) action="stop" ;;
        *) ;;
    esac
done

# ---------- helpers ----------

log() { printf "%b\n" "\033[1;36m[start_native]\033[0m $*"; }
warn() { printf "%b\n" "\033[1;33m[start_native]\033[0m $*" >&2; }
die() { printf "%b\n" "\033[1;31m[start_native]\033[0m $*" >&2; exit 1; }

ensure_postgres() {
    if ! lsof -nP -iTCP:"$PG_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
        die "PostgreSQL 未监听 $PG_PORT，请先 brew services start postgresql@16"
    fi
    log "PostgreSQL OK on $PG_PORT"
}

ensure_redis() {
    if ! lsof -nP -iTCP:"$REDIS_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
        die "Redis 未监听 $REDIS_PORT，请先 brew services start redis"
    fi
    log "Redis OK on $REDIS_PORT"
}

ensure_db_exists() {
    if ! PGPASSWORD="$PG_PASSWORD" psql -U "$PG_USER" -h 127.0.0.1 -p "$PG_PORT" -d "$PG_DB" -tAc "SELECT 1" >/dev/null 2>&1; then
        warn "数据库 $PG_DB 不存在或密码错，尝试创建"
        PGPASSWORD="$PG_PASSWORD" createuser -s "$PG_USER" 2>/dev/null || true
        PGPASSWORD="$PG_PASSWORD" createdb -O "$PG_USER" "$PG_DB" 2>/dev/null || \
            warn "createdb 失败，可能需要手动执行：createdb -O postgres 小炬人"
    fi
}

run_migrations() {
    if ! command -v migrate >/dev/null 2>&1; then
        export PATH="$HOME/go/bin:$PATH"
    fi
    if ! command -v migrate >/dev/null 2>&1; then
        warn "migrate CLI 不在 PATH，跳过迁移（请先 go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@latest）"
        return 0
    fi
    # URL-encode password via Python so special chars (!, @, #, %) survive
    local db_url
    db_url=$(PG_USER="$PG_USER" PG_PASSWORD="$PG_PASSWORD" PG_PORT="$PG_PORT" PG_DB="$PG_DB" python3 << 'PYEOF'
import os, urllib.parse
user = os.environ['PG_USER']
pw = os.environ['PG_PASSWORD']
port = os.environ.get('PG_PORT', '5432')
db = os.environ['PG_DB']
print(f'postgres://{user}:' + urllib.parse.quote(pw) + f'@127.0.0.1:{port}/{db}?sslmode=disable')
PYEOF)
    log "运行数据库迁移"
    (cd "$PROJECT_ROOT" && migrate -path migrations/versioned -database "$db_url" up 2>&1 | tail -5)
}

ensure_redis_password() {
    # Redis 默认无密码；如果 .env 里设了密码就写上。
    if [ -n "$REDIS_PASSWORD" ]; then
        redis-cli -h 127.0.0.1 -p "$REDIS_PORT" CONFIG SET requirepass "$REDIS_PASSWORD" >/dev/null 2>&1 || true
        redis-cli -h 127.0.0.1 -p "$REDIS_PORT" -a "$REDIS_PASSWORD" CONFIG SET requirepass "$REDIS_PASSWORD" >/dev/null 2>&1
    fi
}

start_docreader() {
    local pidfile="$LOG_DIR/docreader.pid"
    if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
        log "DocReader 已在 PID $(cat "$pidfile")"
        return 0
    fi
    if lsof -nP -iTCP:"$DOCREADER_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
        log "DocReader 已在监听 $DOCREADER_PORT（异源）"
        return 0
    fi
    warn "DocReader 未启动。本脚本不内置 docreader 进程（需要 uv + Python 依赖）。"
    warn "如需解析引擎 builtin/markitdown/opendataloader，请另开终端："
    warn "  cd $PROJECT_ROOT/docreader && uv sync && uv run python -m docreader.main"
    return 1
}

start_app() {
    local pidfile="$LOG_DIR/app.pid"
    if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
        log "App 已在 PID $(cat "$pidfile")"
        return 0
    fi
    if lsof -nP -iTCP:"$APP_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
        log "App 已在监听 $APP_PORT（异源）"
        return 0
    fi
    log "启动原生后端 (make dev-app)"
    (cd "$PROJECT_ROOT" && nohup make dev-app > "$LOG_DIR/app.log" 2>&1) &
    local app_pid=$!
    echo "$app_pid" > "$pidfile"
    for i in 1 2 3 4 5 6 7 8 9 10; do
        sleep 3
        if curl -sf "http://127.0.0.1:$APP_PORT/health" >/dev/null 2>&1; then
            log "App 健康 (PID $(cat "$pidfile"))"
            return 0
        fi
    done
    warn "App 启动超时，查看日志：$LOG_DIR/app.log"
    return 1
}

start_bge_proxy() {
    if curl -sf "http://127.0.0.1:$PROXY_PORT/health" >/dev/null 2>&1; then
        log "BGE 代理已就绪 ($PROXY_PORT)"
        return 0
    fi
    warn "BGE 代理未启动（预期由 Ollama 后端持续运行）。脚本不内置启动。"
    warn "手动启动：PYTHON=$PYTHON $PROJECT_ROOT/scripts/bge_proxy.py --ollama-url http://127.0.0.1:11434 --port $PROXY_PORT --model nomic-embed-text"
    return 1
}

start_paddleocr() {
    if curl -sf "http://127.0.0.1:$PADDLEOCR_PORT/health" >/dev/null 2>&1; then
        log "PaddleOCR 已就绪 ($PADDLEOCR_PORT)"
        return 0
    fi
    warn "PaddleOCR 未启动。脚本不内置（依赖 paddleocr + fastapi + pytorch）。"
    warn "手动启动：$PYTHON $PROJECT_ROOT/小炬人/scripts/paddleocr_vl_server.py"
    return 1
}

start_frontend() {
    local pidfile="$LOG_DIR/frontend.pid"
    if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
        log "前端已在 PID $(cat "$pidfile")"
        return 0
    fi
    if lsof -nP -iTCP:"$FRONTEND_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
        log "前端已在监听 $FRONTEND_PORT（异源）"
        return 0
    fi
    log "启动原生前端 (vite dev --port $FRONTEND_PORT)"
    (cd "$PROJECT_ROOT/frontend" && \
        FRONTEND_BACKEND_URL="http://127.0.0.1:$APP_PORT" \
        nohup npm run dev -- --port "$FRONTEND_PORT" --host \
        > "$LOG_DIR/frontend.log" 2>&1) &
    local fe_pid=$!
    echo "$fe_pid" > "$pidfile"
    for i in 1 2 3 4 5; do
        sleep 3
        if lsof -nP -iTCP:"$FRONTEND_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
            log "前端已就绪 ($FRONTEND_PORT)"
            return 0
        fi
    done
    warn "前端启动超时，查看日志：$LOG_DIR/frontend.log"
    return 1
}

register_demo_user() {
    log "尝试注册/确认 demo 账号 test@test.com / test123456"
    local resp
    resp=$(curl -s -m 5 -X POST "http://127.0.0.1:$APP_PORT/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d '{"email":"test@test.com","password":"test123456","username":"test"}' 2>&1)
    if echo "$resp" | grep -q '"success":true'; then
        log "demo 账号注册成功"
    elif echo "$resp" | grep -q 'already exists'; then
        log "demo 账号已存在，跳过注册"
    else
        warn "注册失败（$resp）"
    fi
}

smoke_test() {
    log "端到端冒烟测试"
    local token
    token=$(curl -s -m 5 -X POST "http://127.0.0.1:$APP_PORT/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d '{"email":"test@test.com","password":"test123456"}' \
        | python3 -c "import json,sys; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    [ -z "$token" ] && { warn "登录失败"; return 1; }
    log "登录 OK (token ${#token} chars)"

    local engines
    engines=$(curl -s -m 5 "http://127.0.0.1:$APP_PORT/api/v1/system/parser-engines" \
        -H "Authorization: Bearer $token" | python3 -c "
import json,sys
d=json.load(sys.stdin)
ok=sum(1 for e in d.get('data',[]) if e['Available'])
total=len(d.get('data',[]))
print(f'{ok}/{total}')
")
    log "解析引擎: $engines 可用"

    local emb
    emb=$(curl -s -m 15 -X POST "http://127.0.0.1:$APP_PORT/api/v1/initialization/embedding/test" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d '{"modelName":"bge-m3","baseUrl":"http://127.0.0.1:'$PROXY_PORT'","apiKey":"","provider":"generic","interfaceType":"openai","dimension":768}' \
        | python3 -c "import json,sys; d=json.load(sys.stdin).get('data',{}); print(d.get('available'))" 2>/dev/null)
    log "Embedding 连通性: ${emb:-不可用}"

    log "✅ 端到端 OK"
    log ""
    log "访问地址："
    log "  前端: http://localhost:$FRONTEND_PORT"
    log "  后端: http://localhost:$APP_PORT  (api base: /api/v1)"
    log "  账号: test@test.com / test123456"
    log "  注意：native 模式后端默认绑定 8080（受 config.yaml 限制）"
}

stop_all() {
    log "停止所有原生进程"
    for name in frontend app; do
        local pidfile="$LOG_DIR/$name.pid"
        if [ -f "$pidfile" ]; then
            local pid
            pid="$(cat "$pidfile" 2>/dev/null)"
            if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
                log "stopped $name (PID $pid)"
            fi
            rm -f "$pidfile"
        fi
    done
    # Backend may have been started via make dev-app; clean leftovers
    pkill -f "exe/server" 2>/dev/null || true
    pkill -f "go run.*cmd/server" 2>/dev/null || true
}

start_all() {
    ensure_postgres
    ensure_db_exists
    run_migrations
    ensure_redis
    ensure_redis_password
    start_docreader || true
    start_app
    start_bge_proxy || true
    start_paddleocr || true
    start_frontend
    register_demo_user
    smoke_test
}

case "$action" in
    start) start_all ;;
    stop) stop_all ;;
esac