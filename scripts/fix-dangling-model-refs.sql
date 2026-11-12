-- 修复「知识库 → 模型」的悬空引用（开发机 WeKnora）。
--
-- 背景：2026-11-12 从 434/457 生产迁数据到开发机时，迁了 knowledge_bases /
--   chunks / embeddings 等表，**漏了 models 表**。于是迁入库的
--   embedding_model_id / summary_model_id 指向生产的模型 id，
--   而 dev 的 models 表里查不到 → 悬空。
--
-- 后果：这些库一旦有新文档要算向量（回填 backfill_embeddings /
--   上传后自动索引）就会因为「模型不存在」而失败。
--
-- 巡检：bash scripts/check-kb-vectors.sh  → 第 3 节会列出「⚠ 模型悬空」
--
-- 修法：把悬空引用改指 dev 里**已存在**的等价模型（不新建模型记录）：
--   embedding 悬空 → builtin-bge-m3-local（本机 ollama bge-m3，1024 维，
--                    与 dev 已有的全部向量同维，不会造成维度冲突）
--   summary   悬空 → 75a79565…（MiniMax-M3.1-Flash-Preview，KnowledgeQA，
--                    dev 里已有且在用）
--
-- 已迁移的向量本身是 1024 维（与 builtin-bge-m3-local 一致），所以改指后
-- 旧的向量继续可用，不需要重新回填。
--
-- 执行前会自动备份到 bak_kb_model_20261112；回滚见文件末尾。

\set ON_ERROR_STOP on

BEGIN;

-- 备份原引用（可重复执行；每次覆盖前先删旧备份）
DROP TABLE IF EXISTS bak_kb_model_20261112;
CREATE TABLE bak_kb_model_20261112 AS
SELECT id, name, tenant_id, embedding_model_id, summary_model_id,
       image_processing_config, now() AS fixed_at
FROM knowledge_bases;

-- 1) embedding 模型悬空 → dev 本地 bge-m3
UPDATE knowledge_bases
SET embedding_model_id = 'builtin-bge-m3-local', updated_at = now()
WHERE deleted_at IS NULL
  AND (embedding_model_id = ''
       OR embedding_model_id = 'c08a240a-db1c-4059-9445-3f6922c11d8f');

-- 2) summary 模型悬空 → dev 已有的 KnowledgeQA 模型
UPDATE knowledge_bases
SET summary_model_id = '75a79565-1998-45e0-8315-8f6212149cb4', updated_at = now()
WHERE deleted_at IS NULL
  AND (summary_model_id = ''
       OR summary_model_id IN ('18cca551-6d04-4652-9de7-509bdd4927e7',
                               '8ed218f8-f03a-4ffe-93ec-9635068536b4',
                               'ea8933bc-bd3d-42d2-97aa-3342ac477291'));

COMMIT;

-- 复核：应返回 0 行
SELECT kb.name, kb.id,
       coalesce(m.name, '⚠仍悬空') AS embedding_model,
       coalesce(s.name, '⚠仍悬空') AS summary_model
FROM knowledge_bases kb
LEFT JOIN models m ON m.id = kb.embedding_model_id
LEFT JOIN models s ON s.id = kb.summary_model_id
WHERE kb.deleted_at IS NULL
  AND (m.id IS NULL OR s.id IS NULL);

-- 回滚（若需要）：
-- UPDATE knowledge_bases kb
-- SET embedding_model_id = b.embedding_model_id,
--     summary_model_id   = b.summary_model_id
-- FROM bak_kb_model_20261112 b WHERE b.id = kb.id;
