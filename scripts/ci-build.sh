#!/bin/sh
# CI 构建脚本：构建并推送本项目镜像到内网 registry
#
# 由 .woodpecker/build.yml（NAS 构建机）与 .woodpecker/release.yml（发布兜底）共用。
#
# 环境变量：
#   REG               registry 前缀，默认 100.108.142.49:5000
#   TAG               推送的版本 tag，默认 ${CI_COMMIT_SHA}
#   REPOS_TO_BUILD    只构建这几个仓库（空格分隔）；不设则按下面的规则全跑
#   CI_PIPELINE_FILES 本次提交变更的文件（JSON 数组），用于按需构建（可选）
#   USE_MIRROR        是否使用国内镜像源，默认 1
#
# 镜像源策略：
#   国内镜像源有同步延迟（上游刚发版时可能还没同步上），会让构建直接失败。
#   所以：先用镜像源构建，失败就自动去掉镜像参数、用官方源重试一次
#   （官方源慢，但一定准）。
#
# 注意：所有镜像必须是 linux/amd64 —— 构建机本身是 x86_64，docker build 默认即 amd64。
set -e

REG=${REG:-100.108.142.49:5000}
TAG=${TAG:-${CI_COMMIT_SHA}}
USE_MIRROR=${USE_MIRROR:-1}

# 国内镜像源：NAS 直连官方源极慢（实测 deb.debian.org 35KB/s，一个 apt 包要一分钟；
# pypi / npmjs / static.rust-lang.org 同理）。全部做成可覆盖的变量。
# ⚠️ npm/pip 必须是**完整 URL**（带 https://），只给域名会报 ERR_INVALID_URL。
APK_MIRROR_ARG=${APK_MIRROR_ARG:-mirrors.aliyun.com}          # app（历史命名）
APT_MIRROR=${APT_MIRROR:-mirrors.aliyun.com}                  # docreader / odl / sandbox
NPM_MIRROR_ARG=${NPM_MIRROR_ARG:-https://registry.npmmirror.com}
RUSTUP_MIRROR_ARG=${RUSTUP_MIRROR_ARG:-mirrors.aliyun.com}
CARGO_MIRROR_ARG=${CARGO_MIRROR_ARG:-sparse+https://rsproxy.cn/index/}
PIP_INDEX_URL=${PIP_INDEX_URL:-https://mirrors.aliyun.com/pypi/simple/}
UV_INDEX_URL=${UV_INDEX_URL:-https://mirrors.aliyun.com/pypi/simple/}

# CI_PIPELINE_FILES 是 JSON 数组：["a/b.go","c/d.ts"]
# 去掉括号与引号后按空格/逗号拆成逐个文件
CHANGED=$(echo "${CI_PIPELINE_FILES:-}" | tr -d '[]"' | tr ',' ' ')

# 只在文件列表非空时才做裁剪；拿不到变更列表就全部构建（宁可多构建，不要漏构建）
changed_prefix() {
  [ -n "$CHANGED" ] || return 0
  for f in $CHANGED; do
    case "$f" in "$1"*) return 0 ;; esac
  done
  return 1
}

changed_file() {
  [ -n "$CHANGED" ] || return 0
  for f in $CHANGED; do
    if [ "$f" = "$1" ]; then return 0; fi
  done
  return 1
}

# ── 镜像参数（USE_MIRROR=0 时为空，即用官方源）────────────────
M_APP=""
M_UI=""
M_DOC=""
M_ODL=""
M_SANDBOX=""
if [ "$USE_MIRROR" = "1" ]; then
  M_APP="--build-arg APK_MIRROR_ARG=$APK_MIRROR_ARG --build-arg NPM_MIRROR_ARG=$NPM_MIRROR_ARG --build-arg RUSTUP_MIRROR_ARG=$RUSTUP_MIRROR_ARG --build-arg CARGO_MIRROR_ARG=$CARGO_MIRROR_ARG"
  M_UI="--build-arg NPM_REGISTRY=$NPM_MIRROR_ARG"
  M_DOC="--build-arg APT_MIRROR=$APT_MIRROR --build-arg PIP_INDEX_URL=$PIP_INDEX_URL --build-arg UV_INDEX_URL=$UV_INDEX_URL"
  M_ODL="--build-arg APT_MIRROR=$APT_MIRROR"
  M_SANDBOX="--build-arg APT_MIRROR=$APT_MIRROR --build-arg NPM_REGISTRY=$NPM_MIRROR_ARG"
fi

# 推送镜像：同时打 <tag> 与 latest 两个标签
push() {
  repo=$1
  echo "--- 推送 $repo"
  docker push "$REG/$repo:$TAG"
  docker push "$REG/$repo:latest"
}

build_app() {
  echo "=== 构建 wechatopenai/weknora-app ==="
  if ! docker build \
      --build-arg GOPROXY_ARG="${GOPROXY_ARG:-https://goproxy.cn,direct}" \
      $M_APP \
      -t "$REG/wechatopenai/weknora-app:$TAG" \
      -t "$REG/wechatopenai/weknora-app:latest" \
      -f docker/Dockerfile.app .; then
    echo "!!! 构建失败：可能是国内镜像源未同步，去掉镜像参数用官方源重试一次"
    USE_MIRROR=0
    M_APP=""
    docker build \
      --build-arg GOPROXY_ARG="${GOPROXY_ARG:-https://goproxy.cn,direct}" \
      -t "$REG/wechatopenai/weknora-app:$TAG" \
      -t "$REG/wechatopenai/weknora-app:latest" \
      -f docker/Dockerfile.app .
  fi
  push wechatopenai/weknora-app
}

build_ui() {
  echo "=== 构建 wechatopenai/weknora-ui ==="
  if ! docker build \
      $M_UI \
      -t "$REG/wechatopenai/weknora-ui:$TAG" \
      -t "$REG/wechatopenai/weknora-ui:latest" \
      frontend/; then
    echo "!!! 构建失败：可能是国内镜像源未同步，去掉镜像参数用官方源重试一次"
    docker build \
      -t "$REG/wechatopenai/weknora-ui:$TAG" \
      -t "$REG/wechatopenai/weknora-ui:latest" \
      frontend/
  fi
  push wechatopenai/weknora-ui
}

build_docreader() {
  echo "=== 构建 wechatopenai/weknora-docreader ==="
  if ! docker build \
      $M_DOC \
      -t "$REG/wechatopenai/weknora-docreader:$TAG" \
      -t "$REG/wechatopenai/weknora-docreader:latest" \
      -f docker/Dockerfile.docreader .; then
    echo "!!! 构建失败：可能是国内镜像源未同步，去掉镜像参数用官方源重试一次"
    docker build \
      -t "$REG/wechatopenai/weknora-docreader:$TAG" \
      -t "$REG/wechatopenai/weknora-docreader:latest" \
      -f docker/Dockerfile.docreader .
  fi
  push wechatopenai/weknora-docreader
}

build_sandbox() {
  echo "=== 构建 wechatopenai/weknora-sandbox ==="
  if ! docker build \
      $M_SANDBOX \
      -t "$REG/wechatopenai/weknora-sandbox:$TAG" \
      -t "$REG/wechatopenai/weknora-sandbox:latest" \
      -f docker/Dockerfile.sandbox .; then
    echo "!!! 构建失败：可能是国内镜像源未同步，去掉镜像参数用官方源重试一次"
    docker build \
      -t "$REG/wechatopenai/weknora-sandbox:$TAG" \
      -t "$REG/wechatopenai/weknora-sandbox:latest" \
      -f docker/Dockerfile.sandbox .
  fi
  push wechatopenai/weknora-sandbox
}

build_mcp() {
  echo "=== 构建 weknora-mcp ==="
  docker build \
    -t "$REG/weknora-mcp:$TAG" \
    -t "$REG/weknora-mcp:latest" \
    mcp-server/
  push weknora-mcp
}

build_odl_hybrid() {
  echo "=== 构建 weknora-odl-hybrid ==="
  if ! docker build \
      $M_ODL \
      -t "$REG/weknora-odl-hybrid:$TAG" \
      -t "$REG/weknora-odl-hybrid:latest" \
      -f docker/Dockerfile.odl-hybrid .; then
    echo "!!! 构建失败：可能是国内镜像源未同步，去掉镜像参数用官方源重试一次"
    docker build \
      -t "$REG/weknora-odl-hybrid:$TAG" \
      -t "$REG/weknora-odl-hybrid:latest" \
      -f docker/Dockerfile.odl-hybrid .
  fi
  push weknora-odl-hybrid
}

echo "REG=$REG  TAG=$TAG  USE_MIRROR=$USE_MIRROR"
echo "变更文件=${CHANGED:-<未提供，全部构建>}"

# REPOS_TO_BUILD：指定只构建这几个仓库（发布脚本复用 NAS 产物时用得上）
# 未指定时按「核心镜像每次构建 + 低频镜像按变更文件裁剪」执行
want() {
  if [ -n "$REPOS_TO_BUILD" ]; then
    for r in $REPOS_TO_BUILD; do
      [ "$r" = "$1" ] && return 0
    done
    return 1
  fi
  return 0
}

# 核心镜像：每次都构建
if want wechatopenai/weknora-app; then build_app; fi
if want wechatopenai/weknora-ui; then build_ui; fi

# 低频镜像：只在相关源码变更时构建（装依赖很慢，日常提交不必等）
if want wechatopenai/weknora-docreader \
   && { changed_prefix "docreader/" || changed_file "docker/Dockerfile.docreader"; }; then
  build_docreader
elif want wechatopenai/weknora-docreader; then
  echo "--- 跳过 weknora-docreader（本次未变更相关文件）"
fi

if want wechatopenai/weknora-sandbox \
   && { changed_file "docker/Dockerfile.sandbox" \
        || changed_file "scripts/build_browserskill.sh" \
        || changed_file "scripts/browserskill-release.json"; }; then
  build_sandbox
elif want wechatopenai/weknora-sandbox; then
  echo "--- 跳过 weknora-sandbox（本次未变更相关文件）"
fi

if want weknora-mcp && changed_prefix "mcp-server/"; then
  build_mcp
elif want weknora-mcp; then
  echo "--- 跳过 weknora-mcp（本次未变更相关文件）"
fi

if want weknora-odl-hybrid && changed_file "docker/Dockerfile.odl-hybrid"; then
  build_odl_hybrid
elif want weknora-odl-hybrid; then
  echo "--- 跳过 weknora-odl-hybrid（本次未变更相关文件）"
fi

echo ""
echo "=== 构建完成 ==="
