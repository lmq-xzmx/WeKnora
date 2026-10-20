-- ============================================================
-- Rollback: 000113_tenant_domain
-- ============================================================

-- 删除 tenant_domains 表
DROP TABLE IF EXISTS tenant_domains;

-- 撤销 Tenant.oidc_config 字段（SQLite 不支持 DROP COLUMN，改用备份方式处理）
-- 注意：生产环境如需真正删除列，需要重建表，这里仅标记为废弃
