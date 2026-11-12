#!/bin/bash
# 给迁移到开发机的 434 语文大师库回填向量（backfill_embeddings）。
#
# 为什么需要：生产的向量在 Qdrant（已停用），PG 的 embeddings 表里没数据，
# 迁过来的 chunk 没有向量 → 检索返回空。WeKnora 自带的 maintenance 端点
# 会调当前 embedding 模型（builtin-bge-m3 → 100.96.230.105:3081）重新算。
#
# 关键细节：
#   · 默认批量 200 条算不完 10 分钟（backfillBatchTimeout）→ 整批作废，故用 batch_size=16
#   · maintenance 端点要求 key 同时满足「KB 在白名单」+「有 manage_kbs 能力」，
#     所以脚本自己临时建一把专用 key（白名单仅目标库），**结束即删**
#   · key 明文只在内存与 psql 的 stdin 里，不落任何文件
#
# 用法：bash scripts/backfill-434-embeddings.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$REPO/.env"
KB=e15f1ef8-a664-43c8-9708-40333144072c   # 语文大师（精品资料）（434生产）
BASE=http://localhost:5016
KEY_NAME=maintenance-434-backfill

DB_HOST=$(grep -E "^DB_HOST=" "$ENV_FILE" | cut -d= -f2-)
DB_PORT=$(grep -E "^DB_PORT=" "$ENV_FILE" | cut -d= -f2-)
DB_USER=$(grep -E "^DB_USER=" "$ENV_FILE" | cut -d= -f2-)
DB_NAME=$(grep -E "^DB_NAME=" "$ENV_FILE" | cut -d= -f2-)
export PGPASSWORD=$(grep -E "^DB_PASSWORD=" "$ENV_FILE" | cut -d= -f2-)
PSQL=(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -q)

cleanup() { "${PSQL[@]}" -c "DELETE FROM tenant_api_keys WHERE name='$KEY_NAME';" >/dev/null 2>&1; }
trap cleanup EXIT

echo "=== 0) 临时建一把专用 key（白名单仅目标库，跑完自动删）==="
# token 只在内存里经环境变量传递，不落任何文件
TOKEN=$(python3 - <<'PY'
import hashlib, base64, secrets
print("sk-" + base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip('='))
PY
)
export TOKEN
python3 - <<'PY' | "${PSQL[@]}" -f -
import hashlib, os
token = os.environ["TOKEN"]
h = hashlib.sha256(token.encode()).hexdigest()
print(f"""INSERT INTO tenant_api_keys (tenant_id, name, key_hash, api_key, full_access, knowledge_base_ids, capabilities, scope_type, created_at, updated_at)
VALUES (10100, 'maintenance-434-backfill', '{h}', '{token}', false,
 '["e15f1ef8-a664-43c8-9708-40333144072c"]'::jsonb,
 '["retrieve","manage_kbs"]'::jsonb, 'tenant', now(), now());""")
PY
[ -n "$TOKEN" ] || { echo "生成 key 失败"; exit 1; }

echo "=== 1) 计划 ==="
curl -sS -m 60 -X POST "$BASE/api/v1/knowledge-bases/$KB/maintenance/plan" \
  -H "X-API-Key: $TOKEN" -H "Content-Type: application/json" \
  -d '{"action":"backfill_embeddings"}'
echo

echo "=== 2) 执行回填（batch_size=16）==="
curl -sS -m 60 -X POST "$BASE/api/v1/knowledge-bases/$KB/maintenance/run" \
  -H "X-API-Key: $TOKEN" -H "Content-Type: application/json" \
  -d '{"action":"backfill_embeddings","batch_size":16}'
echo

echo "=== 3) 进度（每 20s 查一次，最多 24 次 ≈ 8 分钟）==="
for i in $(seq 1 24); do
  sleep 20
  curl -sS -m 20 "$BASE/api/v1/knowledge-bases/$KB/maintenance/runs" \
    -H "X-API-Key: $TOKEN" | head -c 300
  echo
done

echo "=== 4) 结束：临时 key 已自动删除 ==="
