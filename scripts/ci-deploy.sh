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

# 生产机清单：名称|地址|部署目录
#   两台都部署（88166038 = zhaoyun，45732375 = zhong-ke-ju）
HOSTS="
zhaoyun|100.102.197.48|/data/WeKnora
zhongkeju|100.96.230.105|/data/WeKnora
"

# 远端 compose 命令：88166038 有 docker-compose(v1)，457 只有 docker compose(v2)，
# 所以在远端自己判断
deploy() {
  name=$1
  addr=$2
  dir=$3
  echo ""
  echo "=== 部署 $name ($addr:$dir) ==="
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
while IFS='|' read -r n a d; do
  [ -z "$n" ] && continue
  echo "  $n -> $a:$d"
done <<EOF
$HOSTS
EOF

# 注意：这里用 here-doc 而不是管道，避免 while 循环跑在子 shell 里
# （子 shell 的失败退出码不会让 set -e 生效，部署失败会被悄悄吞掉）
while IFS='|' read -r n a d; do
  [ -z "$n" ] && continue
  deploy "$n" "$a" "$d"
done <<EOF
$HOSTS
EOF

echo ""
echo "=== 全部部署完成 ==="
