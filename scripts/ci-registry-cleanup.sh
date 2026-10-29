#!/bin/sh
# ─────────────────────────────────────────────────────────────────
# CI registry 清理 —— 防止 CI 产物的旧版本在 NAS registry 无限堆积
#
# 背景：CI 每轮构建推一个新 tag，历史 tag 的镜像层会一直留在
#       registry 的 blobs 目录里（实测曾累积到 6.1GB / 59 个无 tag manifest）。
#       registry 自身不会自动清理，需要显式删除 tag + 跑 garbage-collect。
#
# 安全边界（重要）：
#   1. 只处理本项目 CI 产出的 6 个仓库，绝不触碰第三方镜像。
#   2. 只删「短 SHA 形态」的 CI tag（12 位十六进制），latest / local /
#      语义化版本号一律保留 —— 生产 compose 只用 latest 与 local。
#   3. 每个目标删除前都重新校验：其 digest 不得等于该仓库 latest 的 digest。
#      registry 2.8.3 的 DELETE 只能按 digest，且会连带删除同 digest 的所有
#      tag，因此这一校验是防止打掉 latest 的唯一保险。
#   4. 保留最近 KEEP 个 CI tag（默认 2），足够回滚。
#
# 用法：KEEP=2 sh scripts/ci-registry-cleanup.sh
#       DRY_RUN=1 sh scripts/ci-registry-cleanup.sh   # 只列出将删除的目标，不执行
# ─────────────────────────────────────────────────────────────────
set -e

REG=${REG:-http://100.108.142.49:5000}
KEEP=${KEEP:-2}
DRY_RUN=${DRY_RUN:-0}
CI_REPOS="wechatopenai/weknora-app wechatopenai/weknora-ui wechatopenai/weknora-docreader wechatopenai/weknora-sandbox weknora-mcp weknora-odl-hybrid"
ACC='Accept: application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.index.v1+json'

digest_of() {
  curl -sS -I -H "$ACC" "$REG/v2/$1/manifests/$2" \
    | grep -i docker-content-digest | tr -d '\r' | awk '{print $2}'
}

# 列出形如 12 位十六进制的 CI tag
ci_tags() {
  curl -sS "$REG/v2/$1/tags/list" \
    | tr ',' '\n' \
    | sed -E 's/.*"([0-9a-f]{12})".*/\1/' \
    | grep -E '^[0-9a-f]{12}$' | sort -r
}

for repo in $CI_REPOS; do
  tags=$(ci_tags "$repo")
  [ -z "$tags" ] && continue

  keep=$(echo "$tags" | head -"$KEEP")
  protected=$(digest_of "$repo" latest 2>/dev/null || true)

  for t in $tags; do
    echo "$keep" | grep -qx "$t" && continue

    d=$(digest_of "$repo" "$t")
    [ -z "$d" ] && continue

    if [ "$d" = "$protected" ]; then
      echo "  跳过 $repo:$t —— 与 latest 同 digest，删除会连带打掉 latest"
      continue
    fi

    if [ "$DRY_RUN" = "1" ]; then
      echo "  [dry-run] 将删除 $repo:$t (${d#sha256:})"
    else
      code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$REG/v2/$repo/manifests/$d")
      echo "  删除 $repo:$t (${d#sha256:}) -> HTTP $code"
    fi
  done
done

echo "CI registry 清理完成（保留每个仓库最近 $KEEP 个 CI tag 与 latest/local）。"
echo "如需回收磁盘空间，在 NAS 上执行 registry garbage-collect --delete-untagged。"
