#!/bin/sh
# CI 部署脚本：SSH 到生产机，拉取刚构建的镜像并滚动更新
#
# 由 .woodpecker/deploy.yml（常规发布）与 urgent.yml（紧急发布）共用。
# 部署私钥由 agent 通过 WOODPECKER_BACKEND_DOCKER_VOLUMES 挂到步骤容器的
# /root/.ssh/deploy_ed25519（只有 ECS 上的 agent 挂了这把钥匙）。
#
# 地址用 Tailscale 内网地址（100.x），只有内网不通时才退到公网地址。
set -e

KEY=${KEY:-/root/.ssh/deploy_ed25519}
# ⚠️ 必须带 -n（ssh 不读 stdin）：否则 ssh 会把 while read 循环的输入吃光，
#    导致循环只跑第一台机器、后面的静默跳过。
SSH_OPTS="-n -o StrictHostKeyChecking=accept-new -o BatchMode=yes -o ConnectTimeout=15 -i $KEY"

# 生产机清单：名称|地址|部署目录|版本钉
#   第四段「版本钉」为空 = 沿用机器自己的 .env（跑 :latest）。
#   填了值 = 发布前把 .env 里的镜像版本变量改成它，再 pull/up。
#
# 为什么 43410253 要钉版本：它的 .env 把 WEKNORA_VERSION / FRONTEND_VERSION
# 钉在不可变 tag（prod-20261106），不吃 :latest —— 不显式改 .env 的话，
# `compose pull` 拉回来的还是老镜像，发布等于没发。钉到 commit SHA 是
# 不可变的，回滚只要把 .env 改回旧值（备份留在 .env.bak-*）。
#
#   zhaoyun  = 88166038（docker-compose v1 + PG 未调参的老 compose）
#   zhongkeju= 45732375（compose v2）
#   newprod  = 43410253（新生产机，compose v2 + 不可变 tag）
HOSTS="
zhaoyun|100.102.197.48|/data/WeKnora|
zhongkeju|100.96.230.105|/data/WeKnora|
newprod|100.118.20.125|/data/WeKnora|@RELEASE_SHA@
"

# 本次发布对应的 commit SHA —— 就是 registry 里那批镜像的 tag。
# tag 事件下 CI_COMMIT_SHA 可能是「附注标签对象」的 SHA，与标签指向的
# commit SHA 不同（镜像按 commit SHA 命名），必须解析一次。
RELEASE_SHA=${RELEASE_SHA:-${CI_COMMIT_SHA:-}}
if [ -n "$RELEASE_SHA" ]; then
  if git rev-parse --verify -q "${RELEASE_SHA}^{commit}" >/dev/null 2>&1; then
    RESOLVED=$(git rev-parse "${RELEASE_SHA}^{commit}")
    if [ "$RESOLVED" != "$RELEASE_SHA" ]; then
      echo "RELEASE_SHA=$RELEASE_SHA 是标签对象，解析为 commit $RESOLVED"
      RELEASE_SHA=$RESOLVED
    fi
  fi
  # 短 SHA 是 tag 事件的惯例（gitea 默认），但镜像 tag 用的是完整 SHA
  HOSTS=$(echo "$HOSTS" | sed "s|@RELEASE_SHA@|$RELEASE_SHA|g")
fi

# 远端 compose 命令：88166038 有 docker-compose(v1)，457 只有 docker compose(v2)，
# 所以在远端自己判断
deploy() {
  name=$1
  addr=$2
  dir=$3
  pin=$4
  echo ""
  echo "=== 部署 $name ($addr:$dir${pin:+ 钉版本 $pin}) ==="

  # 钉版本：先把 .env 的镜像版本变量改成目标 commit SHA（改前留备份），
  # 再 pull/up。版本变量在 .env 里已存在才改 —— 不给机器凭空加变量，
  # 免得影响它自己的镜像策略。
  #
  # 这段与 pull/up 分成两条 ssh 命令，而不是用 ${var:+...} 把多行嵌进一条 ——
  # 多行嵌进 ssh 的双引号里，展开后的换行与 && 会被 sh 解析成
  # 「空命令 && ...」，语法直接断。分开反而更好排查：钉版本失败就不往下走。
  if [ -n "$pin" ]; then
    ssh $SSH_OPTS "root@$addr" "cd $dir && \
      cp -a .env .env.bak-\$(date +%F-%H%M) && \
      for k in WEKNORA_VERSION FRONTEND_VERSION SANDBOX_VERSION DOCREADER_VERSION; do \
        if grep -q \"^\$k=\" .env; then \
          sed -i.bak \"s|^\$k=.*|\$k=$pin|\" .env && rm -f .env.bak; \
        fi; \
      done && \
      grep -E '^(WEKNORA|FRONTEND|SANDBOX|DOCREADER)_VERSION=' .env"
  fi

  # 远端自己判断用哪个 compose 命令（v1 / v2 都支持）
  ssh $SSH_OPTS "root@$addr" "cd $dir && \
    if command -v docker-compose >/dev/null 2>&1; then \
      C='docker-compose'; \
    else \
      C='docker compose'; \
    fi && \
    \$C pull && \$C up -d"
  echo "--- $name 部署完成"
}

echo "部署目标："
while IFS='|' read -r n a d p; do
  [ -z "$n" ] && continue
  echo "  $n -> $a:$d${p:+  (钉版本 $p)}"
done <<EOF
$HOSTS
EOF

# 注意：这里用 here-doc 而不是管道，避免 while 循环跑在子 shell 里
# （子 shell 的失败退出码不会让 set -e 生效，部署失败会被悄悄吞掉）
while IFS='|' read -r n a d p; do
  [ -z "$n" ] && continue
  deploy "$n" "$a" "$d" "$p"
done <<EOF
$HOSTS
EOF

echo ""
echo "=== 全部部署完成 ==="
