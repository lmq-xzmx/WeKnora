#!/bin/sh
# CI 部署脚本：SSH 到生产机，拉取刚构建的镜像并滚动更新
#
# 由 .woodpecker/deploy.yml（常规发布）与 urgent.yml（紧急发布）共用。
# 部署私钥由 agent 通过 WOODPECKER_BACKEND_DOCKER_VOLUMES 挂到步骤容器的
# /root/.ssh/deploy_ed25519（只有 ECS 上的 agent 挂了这把钥匙）。
#
# 生产机清单：名称|地址|部署目录
#   地址用 Tailscale 内网地址（100.x），只有内网不通时才退到公网地址。
set -e

KEY=${KEY:-/root/.ssh/deploy_ed25519}
SSH_OPTS="-o StrictHostKeyChecking=accept-new -o BatchMode=yes -o ConnectTimeout=15 -i $KEY"

HOSTS="
zhaoyun|100.102.197.48|/data/WeKnora
"

deploy() {
  name=$1
  addr=$2
  dir=$3
  echo ""
  echo "=== 部署 $name ($addr:$dir) ==="
  ssh $SSH_OPTS "root@$addr" "cd $dir && docker-compose pull && docker-compose up -d"
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
