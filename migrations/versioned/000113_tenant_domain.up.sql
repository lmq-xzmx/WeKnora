-- ============================================================
-- Migration: 000113_tenant_domain
-- Description: 新增 tenant_domains 表（租户域名映射）+ Tenant.oidc_config JSONB 字段
-- 支持方案 D：单实例多租户按域名动态选择 OIDC 配置
-- ============================================================

-- 新增 tenant_domains 表
CREATE TABLE IF NOT EXISTS tenant_domains (
    id INTEGER PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tenant_id BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain VARCHAR(255) NOT NULL UNIQUE,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tenant_domains_domain ON tenant_domains(domain);
CREATE INDEX IF NOT EXISTS idx_tenant_domains_tenant_id ON tenant_domains(tenant_id);

-- Tenant 表新增 oidc_config JSONB 字段（方案 D 动态 OIDC 配置）
-- 注意：SQLite 不支持 JSONB，直接存 TEXT，Go 代码按 JSON 处理
ALTER TABLE tenants ADD COLUMN oidc_config TEXT;
