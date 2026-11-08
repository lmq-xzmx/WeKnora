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
#
#    实测（Woodpecker 3.19 + gitea）：tag 事件的 pipelines.commit 存的就是
#    标签对象 SHA（e471e5f6…），不是 commit SHA（62547aac…）。而部署步骤
#    ci-deploy.sh 钉的是 commit SHA —— 两边不一致，compose pull 必然找不到镜像。
#
#    解析不了就**中止**：宁可不发布，也不能按错误的 tag 推镜像。
if git rev-parse --verify -q "${TAG}^{commit}" >/dev/null 2>&1; then
  COMMIT_SHA=$(git rev-parse "${TAG}^{commit}")
  if [ "$COMMIT_SHA" != "$TAG" ]; then
    echo "TAG=$TAG 是标签对象，解析为 commit $COMMIT_SHA"
    TAG=$COMMIT_SHA
  fi
else
  echo "!!! 无法把 $TAG 解析成 commit —— 镜像会按错误的 tag 推送，部署必定失败，中止" >&2
  echo "!!! （常见原因：步骤容器里没有 git，或 clone 没有取到标签对象）" >&2
  exit 1
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

# registry 开了 Basic 认证（htpasswd 保护全部仓库，匿名访问一律 401）。
#
# ⚠️ curl **不会**读 /root/.docker/config.json —— 裸 curl 查 manifest 必然 401，
# 于是 manifest_exists 对每个仓库都返回「不存在」，复用判断把
# 「所有镜像都没有」当成事实，白白从零构建一遍（多等一小时，还多占构建机磁盘）。
#
# 凭据从宿主注入的 config.json 里取 auth 字段（base64(user:pass)），
# 直接当 Authorization 头用：不解码、不落盘、不回显。
# 拿不到凭据就退回裸 curl（结果 401 → 走从零构建，功能上仍然正确，只是慢）。
AUTH_HEADER=""
cfg="${DOCKER_CONFIG:-/root/.docker}/config.json"
if [ -f "$cfg" ]; then
  b64=$(tr -d ' \t\n\r' < "$cfg" | sed -n "s|.*\"$REG\"[^}]*\"auth\":\"\\([^\"]*\\)\".*|\\1|p")
  if [ -n "$b64" ]; then
    AUTH_HEADER="Authorization: Basic $b64"
  else
    echo "⚠️  $cfg 里没有 $REG 的凭据，manifest 查询会 401" >&2
  fi
else
  echo "⚠️  找不到 $cfg，manifest 查询会 401" >&2
fi

# registry 里有没有这个 tag 的清单
manifest_exists() {
  if [ -n "$AUTH_HEADER" ]; then
    code=$(curl -sS -o /dev/null -w '%{http_code}' -H "$ACC" -H "$AUTH_HEADER" "$REG/v2/$1/manifests/$2" || true)
  else
    code=$(curl -sS -o /dev/null -w '%{http_code}' -H "$ACC" "$REG/v2/$1/manifests/$2" || true)
  fi
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

# 缺的必须补建，不能「沿用现有的 latest」。
#
# 为什么：发布时生产机的 .env 被钉在 commit SHA 上，compose pull 会按 SHA
# 拉**每一个** compose 里引用的镜像。少一个镜像的该 SHA tag，
# 那台机器的 pull 就失败、整台发布失败 —— 而 NAS 的路径裁剪恰恰会跳过
# 「本次没改源码」的仓库（如 docreader）。
# 复用旧代码没问题，但必须把旧代码重新打成这个 SHA 的镜像。
#
# TAG 必须显式传下去：tag 事件下 CI_COMMIT_SHA 可能是附注标签对象的 SHA，
# 与镜像 tag 用的 commit SHA 不是一回事（上面已解析过，这里不能再退回原值）。
echo ""
echo "=== 复用 ${REUSED}"
echo "=== 以下仓库没有 $TAG 版本，需要补建：${MISSING}"
TAG="$TAG" REPOS_TO_BUILD="$MISSING" sh scripts/ci-build.sh

# 补建后复核：发布前保证每个仓库在 $TAG 上都有镜像。
# 不复核的话，缺镜像要等到 deploy 步骤才炸，排查成本高得多。
echo ""
echo "=== 补建后复核"
STILL_MISSING=""
for repo in $REPOS; do
  if manifest_exists "$repo" "$TAG"; then
    echo "  ✅ $repo:$TAG"
  else
    echo "  ❌ $repo:$TAG 仍然不存在"
    STILL_MISSING="$STILL_MISSING $repo"
  fi
done
if [ -n "$STILL_MISSING" ]; then
  echo "!!! 仍有镜像缺失${STILL_MISSING} —— 部署必定失败，中止" >&2
  exit 1
fi
echo "=== 全部镜像在 $TAG 上就绪 ==="
