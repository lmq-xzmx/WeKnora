package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateLegacyStorageBackends(t *testing.T) {
	// 表结构在生产里由 SQL 迁移负责，这里手工建最小可用表，
	// 让用例只覆盖数据迁移逻辑本身。
	db := testutil.OpenTestDB(t, &types.StorageBackend{})
	require.NoError(t, db.Exec(`CREATE TABLE tenants (
		id BIGINT PRIMARY KEY, name TEXT, storage_engine_config JSONB,
		default_storage_backend_id VARCHAR(36), updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
    profile_config JSON,
    generated_profile JSON,
		id TEXT PRIMARY KEY, tenant_id BIGINT, storage_provider_config JSONB,
		storage_backend_id VARCHAR(36), cos_config JSON, updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ
	)`).Error)
	tenantConfig, err := (&types.StorageEngineConfig{DefaultProvider: "local", Local: &types.LocalEngineConfig{PathPrefix: "workspace-a"}}).Value()
	require.NoError(t, err)
	providerConfig, err := (types.StorageProviderConfig{Provider: "local"}).Value()
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO tenants(id, name, storage_engine_config) VALUES (?, ?, ?)", 7, "workspace", tenantConfig).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id, tenant_id, storage_provider_config, cos_config) VALUES (?, ?, ?, ?)", "kb-a", 7, providerConfig, []byte("{}")).Error)

	migrateLegacyStorageBackends(db)

	var backend types.StorageBackend
	require.NoError(t, db.Where("tenant_id = ? AND provider = ? AND legacy_alias = ?", 7, "local", true).First(&backend).Error)
	assert.Equal(t, "workspace-a", backend.Config.PathPrefix)

	var tenantDefault, kbBackend string
	require.NoError(t, db.Raw("SELECT default_storage_backend_id FROM tenants WHERE id = 7").Scan(&tenantDefault).Error)
	require.NoError(t, db.Raw("SELECT storage_backend_id FROM knowledge_bases WHERE id = 'kb-a'").Scan(&kbBackend).Error)
	assert.Equal(t, backend.ID, tenantDefault)
	assert.Equal(t, backend.ID, kbBackend)
}
