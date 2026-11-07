-- KB maintenance runs: audit + observability for knowledge-base maintenance
-- actions (M0 embedding-only backfill and later stages).
--
-- Only adds a new table. No business table (knowledges / chunks / wiki_pages /
-- embeddings) is touched, so rolling this migration back is a plain DROP.
CREATE TABLE IF NOT EXISTS kb_maintenance_runs (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             BIGINT      NOT NULL,
    knowledge_base_id     VARCHAR(36) NOT NULL,
    action                VARCHAR(64) NOT NULL,
    stages                JSONB       NOT NULL DEFAULT '[]'::jsonb,
    status                VARCHAR(16) NOT NULL DEFAULT 'queued',
    affected_documents    INTEGER     NOT NULL DEFAULT 0,
    target_chunks         INTEGER     NOT NULL DEFAULT 0,
    chunks_done           INTEGER     NOT NULL DEFAULT 0,
    embedding_calls       INTEGER     NOT NULL DEFAULT 0,
    llm_calls             INTEGER     NOT NULL DEFAULT 0,
    tokens                BIGINT      NOT NULL DEFAULT 0,
    errors                INTEGER     NOT NULL DEFAULT 0,
    dry_run               BOOLEAN     NOT NULL DEFAULT false,
    message               TEXT        NOT NULL DEFAULT '',
    created_by            VARCHAR(64) NOT NULL DEFAULT '',
    started_at            TIMESTAMPTZ,
    finished_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kb_maint_runs_kb ON kb_maintenance_runs(knowledge_base_id, created_at DESC);
