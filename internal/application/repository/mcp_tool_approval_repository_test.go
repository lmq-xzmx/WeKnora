package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func newMCPToolApprovalTestRepo(t *testing.T) interfaces.MCPToolApprovalRepository {
	t.Helper()
	// 以前这里用的是 SQLite 共享缓存内存库，并把连接池压到 1 条连接来绕开
	// SQLITE_LOCKED。PostgreSQL 没有共享缓存表锁：并发 upsert 走的是
	// ON CONFLICT DO UPDATE 的行级锁，后到的那个会等前一个提交后再取最新行，
	// 因此并发补丁不会互相覆盖，也不需要把连接池压成 1。
	db := testutil.OpenTestDB(t, &types.MCPToolApproval{})
	return NewMCPToolApprovalRepository(db)
}

func boolPtr(v bool) *bool { return &v }

func TestMCPToolApprovalIsEnabledDefaultsTrue(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)

	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, required)
}

func TestMCPToolApprovalUpsertPolicyInsertDefaults(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "only-require", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(true),
	}))
	enabled, err := repo.IsEnabled(ctx, 1, "svc", "only-require")
	require.NoError(t, err)
	require.True(t, enabled, "new rows must stay enabled when only approval is set")
	required, err := repo.IsRequired(ctx, 1, "svc", "only-require")
	require.NoError(t, err)
	require.True(t, required)

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "only-enabled", types.MCPToolPolicyPatch{
		Enabled: boolPtr(false),
	}))
	enabled, err = repo.IsEnabled(ctx, 1, "svc", "only-enabled")
	require.NoError(t, err)
	require.False(t, enabled)
	required, err = repo.IsRequired(ctx, 1, "svc", "only-enabled")
	require.NoError(t, err)
	require.False(t, required, "new rows must keep require_approval=false when only enabled is set")
}

func TestMCPToolApprovalUpsertPolicyPreservesOtherColumn(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		Enabled: boolPtr(false),
	}))
	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(true),
	}))

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled, "approval patch must not re-enable a disabled tool")
	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required)

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		Enabled: boolPtr(true),
	}))
	required, err = repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required, "enabled patch must not clear require_approval")
}

func TestMCPToolApprovalConcurrentColumnPatches(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(false),
		Enabled:         boolPtr(true),
	}))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{Enabled: boolPtr(false)})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{RequireApproval: boolPtr(true)})
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)
	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required)
}
