#!/bin/sh
# 发布脚本：优先**复用** NAS 已经构建好的镜像，没有才在当前机器重建
#
# 背景：
#   日常 push main 时，NAS agent 已经把镜像构建好并推到了 registry
#   （tag = 提交号）。打 tag 发布时没必要在 ECS 上再编译一遍 ——
#   把 NAS 构建好的那个 commit 的镜像重新打成 latest 推上去就行，秒级完成。
#
#   只有「registry 里没有这个 commit 的镜像」时（例如直接给一个没合过
#   main 的提交打 tag），才退回完整构建。
#
# 环境变量：
#   REG   registry 前缀，默认 100.108.142.49:5000
#   TAG   发布对应的提交号，默认 ${CI_COMMIT_SHA}
#
# 用法：sh scripts/ci-release.sh
set -e

REG=${REG:-100.108.142.49:5000}
TAG=${TAG:-${CI_COMMIT_SHA}}
REPOS="wechatopenai/weknora-app wechatopenai/weknora-ui wechatopenai/weknora-docreader wechatopenai/weknora-sandbox weknora-mcp weknora-odl-hybrid"
ACC='Accept: application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.index.v1+json'

# registry 里有没有这个 tag 的清单
manifest_exists() {
  code=$(curl -sS -o /dev/null -w '%{http_code}' -H "$ACC" "$REG/v2/$1/manifests/$2" || true)
  [ "$code" = "200" ]
}

echo "REG=$REG  TAG=$TAG"
echo ""

REUSED=""
MISSING=""
for repo in $REPOS; do
  if manifest_exists "$repo" "$TAG"; then
    echo "--- $repo:$TAG 已有（NAS 构建好的）→ 直接复用"
    docker pull "$REG/$repo:$TAG"
    docker tag "$REG/$repo:$TAG" "$REG/$repo:latest"
    docker push "$REG/$repo:latest"
    REUSED="$REUSED $repo"
  else
    echo "--- $repo:$TAG 不存在 → 需要构建"
    MISSING="$MISSING $repo"
  fi
done

if [ -z "$MISSING" ]; then
  echo ""
  echo "=== 全部复用 NAS 已构建的镜像，未重新编译 ==="
  exit 0
fi

echo ""
echo "=== 以下仓库缺少 $TAG 版本，改为本地构建：$MISSING ==="
REPOS_TO_BUILD="$MISSING" sh scripts/ci-build.sh
