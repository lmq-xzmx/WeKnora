# DuckDB 扩展的预下载镜像（内网 registry）。直连 extensions.duckdb.org 在国内
# 只有 12-24KB/s 且频繁断流，构建必失败；扩展在一台网络通畅的机器上预下载好，
# 做成小镜像进内网，构建时直接 COPY，全程不出内网。
# ⚠️ 版本号必须和 go.mod 的 duckdb-go 版本一致，升级 duckdb-go 要重建这个镜像。
ARG DUCKDB_EXT_IMAGE=100.108.142.49:5000/duckdb-ext:1.5.2

# DuckDB 扩展的预下载镜像（内网 registry）。这个阶段必须声明在
# builder 之前 —— FROM 会切断当前阶段，插在中间会让 COPY 引用到自己。
FROM ${DUCKDB_EXT_IMAGE} AS duckdb-ext

# Build extension and daemon from the same pinned source on the runtime architecture.
FROM --platform=$TARGETPLATFORM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS browserskill
WORKDIR /build
# 这一阶段要装 487MB 的 Debian 包 + Rust 工具链 + pnpm 依赖，
# 直连官方源在国内极慢（实测 deb.debian.org 35KB/s，一个包要一分钟）。
# 三个源都支持用构建参数切到国内镜像，见 scripts/ci-build.sh。
ARG APK_MIRROR_ARG
ARG NPM_MIRROR_ARG
ARG RUSTUP_MIRROR_ARG
ARG CARGO_MIRROR_ARG
RUN if [ -n "$APK_MIRROR_ARG" ]; then \
        sed -i "s@deb.debian.org@${APK_MIRROR_ARG}@g" /etc/apt/sources.list.d/debian.sources; \
    fi && \
    if [ -n "$NPM_MIRROR_ARG" ]; then npm config set registry "$NPM_MIRROR_ARG"; fi && \
    if [ -n "$CARGO_MIRROR_ARG" ]; then \
        mkdir -p /usr/local/cargo && \
        printf '[source.crates-io]\nreplace-with = "mirror"\n\n[source.mirror]\nregistry = "%s"\n' "$CARGO_MIRROR_ARG" > /usr/local/cargo/config.toml; \
    fi && \
    apt-get update && \
    apt-get install -y --no-install-recommends git python3 ca-certificates curl build-essential cmake pkg-config && \
    rm -rf /var/lib/apt/lists/*
ENV RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/cargo/bin:$PATH
# RUSTUP_DIST_SERVER 决定工具链从哪下（官方源在国内很慢）
RUN if [ -n "$RUSTUP_MIRROR_ARG" ]; then \
        export RUSTUP_DIST_SERVER="https://${RUSTUP_MIRROR_ARG}/rustup" \
               RUSTUP_UPDATE_ROOT="https://${RUSTUP_MIRROR_ARG}/rustup/rustup"; \
    fi; \
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain stable
COPY scripts/build_browserskill.sh scripts/browserskill-release.json ./scripts/
ARG TARGETOS
ARG TARGETARCH
RUN bash scripts/build_browserskill.sh /opt/weknora/browserskill "${TARGETOS}/${TARGETARCH}"

# Build stage
# Digest pinned for reproducibility: change only when upgrading Go version
FROM golang:1.26-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS builder

WORKDIR /app

# 通过构建参数接收敏感信息
ARG GOPRIVATE_ARG
ARG GOPROXY_ARG
ARG GOSUMDB_ARG=off
ARG APK_MIRROR_ARG

# 设置Go环境变量
ENV GOPRIVATE=${GOPRIVATE_ARG}
ENV GOPROXY=${GOPROXY_ARG}
ENV GOSUMDB=${GOSUMDB_ARG}

# Install dependencies
RUN if [ -n "$APK_MIRROR_ARG" ]; then \
        sed -i "s@deb.debian.org@${APK_MIRROR_ARG}@g" /etc/apt/sources.list.d/debian.sources; \
    fi && \
    apt-get update && \
    apt-get install -y git build-essential libsqlite3-dev curl

# Install migrate tool
RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Copy go mod files. go.mod replace-points anydoc at ./third_party/anydoc-go,
# so that module's go.mod must exist before `go mod download`.
COPY go.mod go.sum ./
COPY third_party/anydoc-go/go.mod third_party/anydoc-go/go.mod
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/download cmd/download
# 扩展从内网镜像 COPY 进来；go 脚本会检测到已安装而跳过下载
COPY --from=duckdb-ext /root/.duckdb /root/.duckdb
RUN go run cmd/download/duckdb/duckdb.go
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod bash ./scripts/copy-licenses.sh /license-bundle

# Get version and commit info for build injection
ARG VERSION_ARG
ARG COMMIT_ID_ARG
ARG BUILD_TIME_ARG
ARG GO_VERSION_ARG

# Set build-time variables
ENV VERSION=${VERSION_ARG}
ENV COMMIT_ID=${COMMIT_ID_ARG}
ENV BUILD_TIME=${BUILD_TIME_ARG}
ENV GO_VERSION=${GO_VERSION_ARG}

# Link the anydoc parser engine (office docs converted in-process, no
# Python docreader). Default on so Hub / compose images ship a working
# engine; pass WITH_ANYDOC=0 to skip the Rust toolchain (~few minutes and
# ~1 GB of build-stage layers).
ARG WITH_ANYDOC=1
ARG RUSTUP_MIRROR_ARG
ARG CARGO_MIRROR_ARG
ENV RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo
ENV PATH=/usr/local/cargo/bin:$PATH
# rustup 也要走国内镜像：工具链下载走 RUSTUP_DIST_SERVER，
# 安装器本体走 RUSTUP_UPDATE_ROOT（都是官方源时只有 60KB/s，会卡住）
# crates.io 同样换成国内镜像（sparse index，不用克隆整个索引）
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    if [ "$WITH_ANYDOC" = "1" ]; then \
        if [ -n "$RUSTUP_MIRROR_ARG" ]; then \
            export RUSTUP_DIST_SERVER="https://${RUSTUP_MIRROR_ARG}/rustup" \
                   RUSTUP_UPDATE_ROOT="https://${RUSTUP_MIRROR_ARG}/rustup/rustup"; \
        fi; \
        if [ -n "$CARGO_MIRROR_ARG" ]; then \
            mkdir -p /usr/local/cargo && \
            printf '[source.crates-io]\nreplace-with = "mirror"\n\n[source.mirror]\nregistry = "%s"\n' "$CARGO_MIRROR_ARG" > /usr/local/cargo/config.toml; \
        fi; \
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
            | sh -s -- -y --profile minimal --default-toolchain stable && \
        ./scripts/build-anydoc-lib.sh; \
    fi

# Build the application with version info
RUN --mount=type=cache,target=/go/pkg/mod \
    if [ "$WITH_ANYDOC" = "1" ]; then \
        make build-prod GO_BUILD_TAGS=anydoc BINARY_NAME=WeKnora; \
    else \
        make build-prod BINARY_NAME=WeKnora; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod cp -r /go/pkg/mod/github.com/yanyiwu/ /app/yanyiwu/

# Final stage
# Digest pinned for reproducibility
FROM debian:12.12-slim@sha256:d5d3f9c23164ea16f31852f95bd5959aad1c5e854332fe00f7b3a20fcc9f635c

WORKDIR /app

ARG APK_MIRROR_ARG
# PyPI 镜像（uv 的 fallback 安装路径用；留空用官方 pypi.org）。
ARG UV_INDEX_URL_ARG

# Pairing derives the gateway URL from the user's page origin by default.
ENV BROWSERSKILL_BINARY=/opt/weknora/browserskill/bsk \
    BROWSERSKILL_EXTENSION_PATH=/opt/weknora/browserskill/browser-skill-weknora-0.3.1.zip
COPY --from=browserskill /opt/weknora/browserskill /opt/weknora/browserskill

# Create a non-root user first
RUN useradd -m -s /bin/bash appuser

# 装 ca-certificates 也要走镜像：官方源在国内只有 25KB/s，
# 光索引+包就要 5 分钟（实测）。源的 URI 是 http://，不涉及 TLS，
# 不存在「必须先从官方源装证书才能用镜像」的问题。
RUN if [ -n "$APK_MIRROR_ARG" ]; then \
        sed -i "s@deb.debian.org@${APK_MIRROR_ARG}@g" /etc/apt/sources.list.d/debian.sources; \
    fi && \
    apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# 装其余运行时依赖（已在上面切到镜像）
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential postgresql-client default-mysql-client tzdata sed curl bash vim wget \
        libsqlite3-0 \
        python3 python3-pip python3-dev libffi-dev libssl-dev \
        nodejs npm \
        gosu \
        ffmpeg && \
    python3 -m pip install --break-system-packages --upgrade pip setuptools wheel && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install uv as its own layer so a failure here can be retried without
# re-running the apt step above. astral.sh resolves to the GitHub Releases
# CDN (releases.astral.sh), which is unreliable on some networks; when it
# fails we fall back to installing uv from PyPI (optionally via a mirror).
RUN mkdir -p /home/appuser/.local/bin && \
    ( if [ -n "$UV_INDEX_URL_ARG" ]; then \
        # 有 pypi 镜像就直接走 pip：官方安装器要访问 releases.astral.sh，
        # 那个地址从国内机器上不可达（实测 NAS 和 ECS 都拉不动，内层下载也没超时）
        # uv 的 wheel 里带 uv / uvx 两个独立脚本，pip 会分别装到 /usr/local/bin，
        # 不需要（也不能）再手动 ln —— uv 和 uvx 同源，ln 会报 "same file" 并返回 1。
        python3 -m pip install --break-system-packages --index-url="$UV_INDEX_URL_ARG" uv ; \
      else \
        timeout 120 curl -LsSf --retry 3 --retry-delay 2 https://astral.sh/uv/install.sh -o /tmp/uv-install.sh \
        && CARGO_HOME=/home/appuser/.cargo UV_INSTALL_DIR=/home/appuser/.local/bin sh /tmp/uv-install.sh ; \
      fi ) && \
    ln -sf /home/appuser/.local/bin/uvx /usr/local/bin/uvx 2>/dev/null || true ; \
    chown -R appuser:appuser /home/appuser && \
    chmod +x /usr/local/bin/uvx

# Create data directories and set permissions
RUN mkdir -p /data/files && \
    chown -R appuser:appuser /app /data/files

# Copy migrate tool from builder stage
COPY --from=builder /go/bin/migrate /usr/local/bin/
COPY --from=builder /app/yanyiwu/ /go/pkg/mod/github.com/yanyiwu/

# Copy the binary from the builder stage
COPY --from=builder /app/config ./config
COPY --from=builder /app/scripts ./scripts
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/dataset/samples ./dataset/samples
COPY --from=builder /root/.duckdb /home/appuser/.duckdb
COPY --from=builder /app/WeKnora .
COPY --from=builder /license-bundle/ ./

# Copy and make entrypoint script executable
COPY --from=builder /app/scripts/docker-entrypoint.sh ./scripts/docker-entrypoint.sh

# Make scripts executable
RUN chmod +x ./scripts/*.sh

# Expose ports
EXPOSE 8080


ENTRYPOINT ["./scripts/docker-entrypoint.sh"]
CMD ["./WeKnora"]
