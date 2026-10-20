-- ============================================================
-- Migration: 000114_delegation_token
-- Description: 新增 delegation_tokens 表，支持租户授权第三方临时访问
-- ============================================================

CREATE TABLE IF NOT EXISTS delegation_tokens (
    id INTEGER PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    scopes TEXT NOT NULL DEFAULT '[]',
    allowed_ips TEXT NOT NULL DEFAULT '[]',
    expires_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP,
    created_by VARCHAR(128) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_delegation_tokens_tenant_id ON delegation_tokens(tenant_id);
CREATE INDEX IF NOT EXISTS idx_delegation_tokens_token_hash ON delegation_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_delegation_tokens_expires_at ON delegation_tokens(expires_at);
