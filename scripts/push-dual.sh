#!/bin/sh
# 双推：gitea（CI 源，生产机默认从这里拉镜像）+ github（镜像/备份）
#
# 用法：
#   sh scripts/push-dual.sh            # 推当前分支到两个远端的同名分支
#   sh scripts/push-dual.sh main       # 推当前 HEAD 到两个远端的 main
#
# 装成 git alias（一次配置，以后一条命令）：
#   git config --global alias.push-dual '!sh scripts/push-dual.sh'
#   git push-dual main
#
# 两个远端必须是同一个提交（推完自动校验），避免 gitea 与 github 悄悄分叉。
set -e

BRANCH=${1:-$(git rev-parse --abbrev-ref HEAD)}
REMOTE_GITEA=${REMOTE_GITEA:-gitea}
REMOTE_GITHUB=${REMOTE_GITHUB:-origin}

if ! git remote get-url "$REMOTE_GITEA" >/dev/null 2>&1; then
  echo "找不到远端 $REMOTE_GITEA" >&2
  exit 1
fi
if ! git remote get-url "$REMOTE_GITHUB" >/dev/null 2>&1; then
  echo "找不到远端 $REMOTE_GITHUB" >&2
  exit 1
fi

echo "=== 双推 $BRANCH ==="
echo "--- $REMOTE_GITEA (CI 源)"
git push "$REMOTE_GITEA" "HEAD:$BRANCH"
echo "--- $REMOTE_GITHUB (镜像)"
git push "$REMOTE_GITHUB" "HEAD:$BRANCH"

echo ""
echo "=== 校验两端是否同一提交 ==="
LOCAL=$(git rev-parse HEAD)
A=$(git ls-remote "$REMOTE_GITEA" "refs/heads/$BRANCH" | awk '{print $1}')
B=$(git ls-remote "$REMOTE_GITHUB" "refs/heads/$BRANCH" | awk '{print $1}')

printf "  本地      %s\n" "$LOCAL"
printf "  %-9s %s\n" "$REMOTE_GITEA" "$A"
printf "  %-9s %s\n" "$REMOTE_GITHUB" "$B"

if [ "$LOCAL" = "$A" ] && [ "$LOCAL" = "$B" ]; then
  echo "  ✓ 两端一致"
else
  echo "  ✗ 两端不一致，请检查" >&2
  exit 1
fi
