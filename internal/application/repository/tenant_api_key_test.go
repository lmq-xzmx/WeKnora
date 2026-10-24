package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTenantAPIKeyRepositoryPersistsUTCExpiry(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")

	db := testutil.OpenTestDB(t, &types.TenantAPIKey{})

	repo := NewTenantAPIKeyRepository(db)
	ctx := context.Background()

	expiresAt := time.Unix(time.Now().UTC().Add(5*time.Second).Unix(), 0).UTC()
	tenantID := uint64(42)
	key := &types.TenantAPIKey{
		TenantID:   &tenantID,
		ScopeType:  types.APIKeyScopeTenant,
		Name:       "integration",
		KeyHash:    "hash-expiry",
		APIKey:     "sk-test",
		FullAccess: true,
		ExpiresAt:  &expiresAt,
	}
	require.NoError(t, repo.CreateAPIKey(ctx, key))

	loaded, err := repo.GetAPIKeyByHash(ctx, key.KeyHash)
	require.NoError(t, err)
	require.NotNil(t, loaded.ExpiresAt)
	// PG 的 expires_at 是 timestamptz：落盘一律归一到 UTC，但驱动读回时给的是
	// 会话时区（本机 TZ=Asia/Shanghai）的 time.Location；SQLite 驱动恰好固定
	// 回 UTC。所以 PG 上断言「UTC 墙钟一致」而不是断言 Location 指针，
	// 后者测的是驱动行为而不是过期时间本身。
	require.Equal(t, expiresAt.Format(time.RFC3339), loaded.ExpiresAt.UTC().Format(time.RFC3339),
		"过期时间必须按 UTC 墙钟往返，不能被本地时区平移")
	require.True(t, loaded.ExpiresAt.Equal(expiresAt))
}

// TestTenantAPIKeyRepositoryUpdateIsTenantScoped 验证通用更新不会越过租户边界。
// 输入同租户和其他租户的 Key；前者更新全部可配置字段，后者必须返回未找到。
func TestTenantAPIKeyRepositoryUpdateIsTenantScoped(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.TenantAPIKey{})
	repo := NewTenantAPIKeyRepository(db)
	ctx := context.Background()
	tenant42, tenant43 := uint64(42), uint64(43)
	keys := []*types.TenantAPIKey{
		{TenantID: &tenant42, ScopeType: types.APIKeyScopeTenant, Name: "scoped", KeyHash: "hash-scoped", APIKey: "sk-scoped"},
		{TenantID: &tenant43, ScopeType: types.APIKeyScopeTenant, Name: "other", KeyHash: "hash-other", APIKey: "sk-other"},
		{TenantID: &tenant42, ScopeType: types.APIKeyScopeTenant, Name: "full", KeyHash: "hash-full", APIKey: "sk-full", FullAccess: true},
	}
	for _, key := range keys {
		require.NoError(t, repo.CreateAPIKey(ctx, key))
	}

	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	updated, err := repo.UpdateAPIKey(ctx, tenant42, keys[0].ID, &types.TenantAPIKey{
		Name: "updated", FullAccess: false,
		KnowledgeBaseIDs: types.StringArray{"kb-1", "kb-2"},
		Capabilities:     types.StringArray{"retrieve", "chat"},
		ExpiresAt:        &expiresAt,
	})
	require.NoError(t, err)
	require.Equal(t, "updated", updated.Name)
	require.Equal(t, types.StringArray{"kb-1", "kb-2"}, updated.KnowledgeBaseIDs)
	require.Equal(t, types.StringArray{"retrieve", "chat"}, updated.Capabilities)
	require.NotNil(t, updated.ExpiresAt)
	require.True(t, updated.ExpiresAt.Equal(expiresAt))

	_, err = repo.UpdateAPIKey(ctx, tenant42, keys[1].ID, &types.TenantAPIKey{Name: "blocked"})
	require.ErrorIs(t, err, ErrTenantAPIKeyNotFound)

	full, err := repo.UpdateAPIKey(ctx, tenant42, keys[2].ID, &types.TenantAPIKey{
		Name: "full updated", FullAccess: false, Capabilities: types.StringArray{"retrieve"},
	})
	require.NoError(t, err)
	require.False(t, full.FullAccess)
}
