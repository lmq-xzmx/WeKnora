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

# ⚠️ tag 事件下 CI_COMMIT_SHA 可能是「附注标签对象」的 SHA（`git tag -a`），
#    而不是标签指向的 commit 的 SHA —— 两者不同，会导致复用判断全部落空、
#    白白从零构建一遍。镜像 tag 是按 commit SHA 命名的，这里统一解析成 commit SHA。
if git rev-parse --verify -q "${TAG}^{commit}" >/dev/null 2>&1; then
  COMMIT_SHA=$(git rev-parse "${TAG}^{commit}")
  if [ "$COMMIT_SHA" != "$TAG" ]; then
    echo "TAG=$TAG 是标签对象，解析为 commit $COMMIT_SHA"
    TAG=$COMMIT_SHA
  fi
fi
# ⚠️ 不含 weknora-odl-hybrid：它现在由生产端推送维护（registry 的 :local 就是
#    生产在跑的那份），CI 发布时既不复用也不覆盖它，免得把 :local 换成旧镜像。
REPOS="wechatopenai/weknora-app wechatopenai/weknora-ui wechatopenai/weknora-docreader wechatopenai/weknora-sandbox weknora-mcp"
ACC='Accept: application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.index.v1+json'

# 缺 curl 会让下面的判断全部失败（还不报错，最难查）——先明确检查
command -v curl >/dev/null 2>&1 || {
  echo "缺少 curl：docker:cli 镜像里没有，请在步骤里先 apk add --no-cache curl" >&2
  exit 1
}

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
    echo "--- $repo:$TAG 不存在"
    MISSING="$MISSING $repo"
  fi
done

if [ -z "$MISSING" ]; then
  echo ""
  echo "=== 全部复用 NAS 已构建的镜像，未重新编译 ==="
  exit 0
fi

# 一个都没有 → 说明这个提交从来没构建过（不是「按路径裁剪掉了」），必须从零构建
if [ -z "$REUSED" ]; then
  echo ""
  echo "=== registry 里没有 $TAG 的任何镜像 → 从零构建 ==="
  sh scripts/ci-build.sh
  exit 0
fi

# 有一部分 → 说明 NAS 的路径裁剪本来就只构建了部分镜像。
# 缺的这些镜像「当前版本」就是现有的 latest（路径没变，镜像也不用变），
# 这里不重建，直接沿用 —— 重建既慢又没意义。
echo ""
echo "=== 复用 ${REUSED}"
echo "=== 以下仓库没有 $TAG 版本，沿用现有的 latest（NAS 的路径裁剪判定它们本次无需重建）：$MISSING"
echo "=== 本次发布未重新编译 ==="
