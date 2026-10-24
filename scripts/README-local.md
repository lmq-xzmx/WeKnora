# 本机部署 WeKnora 后端

WeKnora 后端**不跑 Docker 镜像**，直接用本机编译的 Go 二进制；只有标准组件走 Docker。

## 为什么

改一次 WeKnora 代码就要重建 2.3GB 的 `weknora-app` 镜像。镜像构建里 browserskill 阶段要拉
rustup 工具链、duckdb 扩展要从 `extensions.duckdb.org` 拉，单次 20 分钟以上且容易卡住。
本机编译约 4 分钟，改完直接重启进程就行。

## 拓扑

| 组件 | 位置 | 地址 |
|---|---|---|
| WeKnora 后端 | 本机进程 | `127.0.0.1:5016` |
| WeKnora 前端 | Docker `WeKnora-frontend` | `127.0.0.1:5015` |
| postgres | Docker `WeKnora-postgres` | `127.0.0.1:5433` |
| redis | Docker `WeKnora-redis` | `127.0.0.1:16390` |
| minio | Docker `WeKnora-minio` | `127.0.0.1:9000` |
| docreader | Docker `WeKnora-docreader` | `127.0.0.1:15051` |

redis 映射到 16390 而不是默认 6379 —— 主机上的 6379 已经被 `geo-flow-redis` 占用。

## 启停

```bash
cd /Users/xzmx/Downloads/my-project/WeKnora

# 起（脚本检测到源码比二进制新会自动重新编译）
./scripts/run-local.sh --bg

# 前台跑，看实时日志
./scripts/run-local.sh

# 停
kill $(pgrep -f '/tmp/WeKnora-local')
```

日志固定写到 `/tmp/weknora-local.log`。

## 重建基础设施容器

```bash
docker compose up -d redis docreader
```

只重建 frontend（改 nginx 相关配置后）：

```bash
docker compose up -d --no-deps --force-recreate frontend
```

**不要跑 `docker compose up -d`**（不带 `--no-deps`）—— 它会连带拉起 `app` 容器，
和本机进程抢 :5016 端口，导致启动失败。

## .env 里已改的配置

`.env` 不在 git 跟踪里，本机部署需要这些值：

```
DB_HOST=127.0.0.1
DB_PORT=5433
REDIS_ADDR=127.0.0.1:16390
REDIS_PORT=16390
MINIO_ENDPOINT=127.0.0.1:9000
DOCREADER_ADDR=127.0.0.1:15051
LOCAL_STORAGE_BASE_DIR=.local-data/files
GEO_KNOWLEDGE_WEBHOOK_URL=http://127.0.0.1:5002/api/v1/webhook/weknora
```

两个必须留意的：

- **`JWT_SECRET` 必须有稳定值。** 留空的话 WeKnora 启动时随机生成签名密钥，
  所有已签发的 JWT（含 GEO-K 换发来的 WeKnora token）在重启后全部失效。
- **`SSRF_WHITELIST_EXTRA` 要含 OIDC 端点。** OIDC 端点是 IP 形式的 Casdoor 地址，
  不在白名单会被 SSRF 校验拦掉，`/api/v1/auth/oidc/token-exchange` 直接 401。

## frontend 反代为什么用 IP 字面量

`docker-compose.override.yml` 里 frontend 的 `APP_HOST` 写成 `192.168.65.254`
（Docker Desktop 固定的 host-gateway），**不能写 `host.docker.internal`**。

nginx 用的是变量式 `proxy_pass`（`set $wk_upstream`），每次请求通过
`resolver 127.0.0.11` 重新解析域名。Docker 内嵌 DNS 不认 `host.docker.internal`
——它只存在于容器的 `/etc/hosts`，而 nginx 不查 hosts。写名字的后果是
`could not be resolved (3: Host not found)` → 502。
