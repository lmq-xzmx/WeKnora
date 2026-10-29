#!/bin/sh
# CI 构建脚本：构建并推送本项目镜像到内网 registry
#
# 由 .woodpecker/ 下的 build.yml（NAS 构建机）与 urgent.yml（紧急，ECS 构建机）共用，
# 保证两条路径构建出的镜像完全一致。
#
# 环境变量：
#   REG               registry 前缀，默认 100.108.142.49:5000
#   TAG               推送的版本 tag，默认 ${CI_COMMIT_SHA}
#   CI_PIPELINE_FILES 本次提交变更的文件（逗号分隔），用于按需构建（可选）
#
# 注意：所有镜像必须是 linux/amd64 —— 构建机本身是 x86_64，docker build 默认即 amd64。
set -e

REG=${REG:-100.108.142.49:5000}
TAG=${TAG:-${CI_COMMIT_SHA}}

# 国内镜像源：NAS 直连官方源极慢（实测 deb.debian.org 35KB/s，一个 apt 包要一分钟；
# pypi / npmjs / static.rust-lang.org 同理）。全部做成可覆盖的变量。
APK_MIRROR_ARG=${APK_MIRROR_ARG:-mirrors.aliyun.com}
NPM_MIRROR_ARG=${NPM_MIRROR_ARG:-registry.npmmirror.com}
RUSTUP_MIRROR_ARG=${RUSTUP_MIRROR_ARG:-mirrors.aliyun.com}
APT_MIRROR=${APT_MIRROR:-http://mirrors.aliyun.com}

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

# 推送镜像：同时打 <tag> 与 latest 两个标签
push() {
  repo=$1
  echo "--- 推送 $repo"
  docker push "$REG/$repo:$TAG"
  docker push "$REG/$repo:latest"
}

build_app() {
  echo "=== 构建 wechatopenai/weknora-app ==="
  docker build \
    --build-arg GOPROXY_ARG="${GOPROXY_ARG:-https://goproxy.cn,direct}" \
    --build-arg APK_MIRROR_ARG="$APK_MIRROR_ARG" \
    --build-arg NPM_MIRROR_ARG="$NPM_MIRROR_ARG" \
    --build-arg RUSTUP_MIRROR_ARG="$RUSTUP_MIRROR_ARG" \
    -t "$REG/wechatopenai/weknora-app:$TAG" \
    -t "$REG/wechatopenai/weknora-app:latest" \
    -f docker/Dockerfile.app .
  push wechatopenai/weknora-app
}

build_ui() {
  echo "=== 构建 wechatopenai/weknora-ui ==="
  docker build \
    --build-arg NPM_REGISTRY="$NPM_MIRROR_ARG" \
    -t "$REG/wechatopenai/weknora-ui:$TAG" \
    -t "$REG/wechatopenai/weknora-ui:latest" \
    frontend/
  push wechatopenai/weknora-ui
}

build_docreader() {
  echo "=== 构建 wechatopenai/weknora-docreader ==="
  docker build \
    --build-arg APT_MIRROR="$APT_MIRROR" \
    -t "$REG/wechatopenai/weknora-docreader:$TAG" \
    -t "$REG/wechatopenai/weknora-docreader:latest" \
    -f docker/Dockerfile.docreader .
  push wechatopenai/weknora-docreader
}

build_sandbox() {
  echo "=== 构建 wechatopenai/weknora-sandbox ==="
  docker build \
    -t "$REG/wechatopenai/weknora-sandbox:$TAG" \
    -t "$REG/wechatopenai/weknora-sandbox:latest" \
    -f docker/Dockerfile.sandbox .
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
  docker build \
    -t "$REG/weknora-odl-hybrid:$TAG" \
    -t "$REG/weknora-odl-hybrid:latest" \
    -f docker/Dockerfile.odl-hybrid .
  push weknora-odl-hybrid
}

echo "REG=$REG  TAG=$TAG"
echo "变更文件=${CHANGED:-<未提供，全部构建>}"

# 核心镜像：每次都构建
build_app
build_ui

# 低频镜像：只在相关源码变更时构建（装依赖很慢，日常提交不必等）
if changed_prefix "docreader/" || changed_file "docker/Dockerfile.docreader"; then
  build_docreader
else
  echo "--- 跳过 weknora-docreader（本次未变更相关文件）"
fi

if changed_file "docker/Dockerfile.sandbox" \
   || changed_file "scripts/build_browserskill.sh" \
   || changed_file "scripts/browserskill-release.json"; then
  build_sandbox
else
  echo "--- 跳过 weknora-sandbox（本次未变更相关文件）"
fi

if changed_prefix "mcp-server/"; then
  build_mcp
else
  echo "--- 跳过 weknora-mcp（本次未变更相关文件）"
fi

if changed_file "docker/Dockerfile.odl-hybrid"; then
  build_odl_hybrid
else
  echo "--- 跳过 weknora-odl-hybrid（本次未变更相关文件）"
fi

echo ""
echo "=== 构建完成 ==="
