#!/bin/bash
# 知识库向量健康巡检（只读，不改任何数据）。
#
# 为什么需要：WeKnora 的检索**静默失败** —— chunk 落库了但 embeddings 没落，
# 检索照样返回 HTTP 200 + 空结果列表，从接口上完全看不出是「没向量」。
# 2026-11-12 迁 434 数据时就踩过这个坑（生产向量在已停用的 Qdrant 里，
# PG 的 embeddings 表几乎没数据），排查花了好几轮。
#
# 巡检项：
#   1. 每个库：chunk 数 vs embedding 数 → 缺向量的比例
#   2. embedding 的 dimension 分布 → 与该库 embedding_model 声明的 dimension 是否一致
#   3. embedding_model 指向哪里（base_url / source）→ 开发机能不能解析
#      + 模型记录是否**悬空**（knowledge_bases.embedding_model_id 在 models 里查不到）
#   4. indexing_strategy 的 vector_enabled / keyword_enabled → 检索开关有没有开
#   5. 孤儿 embeddings（knowledge_base_id 指向已软删的库）→ 清理时的候选
#
# 用法：bash scripts/check-kb-vectors.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$REPO/.env"
[ -f "$ENV_FILE" ] || { echo "找不到 $ENV_FILE"; exit 1; }

DB_HOST=$(grep -E "^DB_HOST=" "$ENV_FILE" | cut -d= -f2-)
DB_PORT=$(grep -E "^DB_PORT=" "$ENV_FILE" | cut -d= -f2-)
DB_USER=$(grep -E "^DB_USER=" "$ENV_FILE" | cut -d= -f2-)
DB_NAME=$(grep -E "^DB_NAME=" "$ENV_FILE" | cut -d= -f2-)
export PGPASSWORD=$(grep -E "^DB_PASSWORD=" "$ENV_FILE" | cut -d= -f2-)

psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 <<'SQL'
\pset border 2
\echo '=== 1) 每个库的 chunk / embedding 覆盖（缺向量 → 检索静默返空） ==='
SELECT
  kb.name                                       AS 库,
  kb.id                                         AS kb_id,
  coalesce(c.n, 0)                              AS chunks,
  coalesce(e.n, 0)                              AS embeddings,
  CASE WHEN coalesce(c.n, 0) = 0               THEN '-（空库）'
       WHEN coalesce(e.n, 0) >= coalesce(c.n, 0) THEN 'OK'
       ELSE '⚠ 缺 ' || (c.n - e.n)::text || ' 向量'
  END                                           AS 向量覆盖
FROM knowledge_bases kb
LEFT JOIN (
  SELECT knowledge_base_id, count(*) n FROM chunks
  WHERE deleted_at IS NULL GROUP BY 1
) c ON c.knowledge_base_id = kb.id
LEFT JOIN (
  SELECT knowledge_base_id, count(*) n FROM embeddings GROUP BY 1
) e ON e.knowledge_base_id = kb.id
WHERE kb.deleted_at IS NULL
ORDER BY kb.name;

\echo ''
\echo '=== 2) embedding 维度 vs 模型声明（不一致 → 向量检索对不上） ==='
SELECT
  kb.name                                AS 库,
  e.dimension                            AS 实际维度,
  count(*)                               AS 条数,
  coalesce(m.parameters->'embedding_parameters'->>'dimension', '?') AS 模型声明,
  CASE
    WHEN m.id IS NULL                                       THEN '⚠ 模型记录缺失'
    WHEN (m.parameters->'embedding_parameters'->>'dimension')::int = e.dimension THEN '一致'
    ELSE '⚠ 不一致'
  END                                    AS 比对
FROM embeddings e
JOIN knowledge_bases kb ON kb.id = e.knowledge_base_id AND kb.deleted_at IS NULL
LEFT JOIN models m ON m.id = kb.embedding_model_id
GROUP BY kb.name, e.dimension, m.id, m.parameters->'embedding_parameters'->>'dimension'
ORDER BY kb.name, e.dimension;

\echo ''
\echo '=== 3) 各库 embedding 模型指向（模型记录悬空 → 回填/新文档处理会失败） ==='
SELECT
  kb.name       AS 库,
  kb.embedding_model_id AS 模型id,
  m.name        AS 模型名,
  m.source      AS 来源,
  coalesce(m.parameters->'embedding_parameters'->>'dimension', '?') AS 维度,
  coalesce(m.parameters->>'base_url', '') AS base_url,
  CASE WHEN m.id IS NULL THEN '⚠ 模型悬空（models 表里查不到）'
       WHEN m.source = 'remote' THEN '远程网关'
       ELSE '本地' END AS 说明
FROM knowledge_bases kb
LEFT JOIN models m ON m.id = kb.embedding_model_id
WHERE kb.deleted_at IS NULL
ORDER BY (m.id IS NULL) DESC, kb.name;

\echo ''
\echo '=== 3b) summary 模型悬空（摘要/答案生成会失败） ==='
SELECT
  kb.name AS 库,
  kb.summary_model_id AS 悬空的模型id,
  s.name AS 实际模型
FROM knowledge_bases kb
LEFT JOIN models s ON s.id = kb.summary_model_id
WHERE kb.deleted_at IS NULL AND s.id IS NULL
ORDER BY kb.name;

\echo ''
\echo '=== 4) 检索开关（vector_enabled / keyword_enabled 关了 → 检索返空） ==='
SELECT
  kb.name AS 库,
  coalesce(kb.indexing_strategy->>'vector_enabled', '?')  AS 向量,
  coalesce(kb.indexing_strategy->>'keyword_enabled', '?') AS 关键词,
  coalesce(kb.indexing_strategy->>'wiki_enabled', '?')    AS wiki
FROM knowledge_bases kb
WHERE kb.deleted_at IS NULL
ORDER BY kb.name;

\echo ''
\echo '=== 5) 孤儿 embeddings（指向已软删的库 —— 清理候选） ==='
SELECT
  kb.name AS 已删除的库,
  count(*) AS 残留向量数
FROM embeddings e
JOIN knowledge_bases kb ON kb.id = e.knowledge_base_id
WHERE kb.deleted_at IS NOT NULL
GROUP BY kb.name
ORDER BY count(*) DESC;
SQL
