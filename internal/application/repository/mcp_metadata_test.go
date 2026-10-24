package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMCPMetadataPersistsCompleteScopedSnapshotsAndRejectsOlderWrites(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.MCPMetadata{})
	repo := &mcpServiceRepository{db: db}
	ctx := context.Background()
	newer := &types.MCPMetadata{
		TenantID:          1,
		ServiceID:         "svc",
		Principal:         "user:a",
		ConfigFingerprint: "new",
		SyncedAt:          time.Now().UTC(),
		Instructions:      "Server instructions",
		Tools: []*types.MCPTool{
			{
				Name:        "查询",
				Description: "Full original description",
				InputSchema: json.RawMessage(
					`{"type":"object","oneOf":[{"required":["id"]}],` +
						`"properties":{"id":{"type":"string"}},"additionalProperties":false}`,
				),
			},
		},
	}
	require.NoError(t, repo.SaveMetadata(ctx, newer))
	older := *newer
	older.SyncedAt = newer.SyncedAt.Add(-time.Minute)
	older.Tools = []*types.MCPTool{}
	require.NoError(t, repo.SaveMetadata(ctx, &older))
	got, err := repo.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	requireToolsEqual(t, newer.Tools, got.Tools)
	require.Equal(t, newer.Instructions, got.Instructions)
	for _, key := range []struct {
		tenant             uint64
		service, principal string
	}{{2, "svc", "user:a"}, {1, "svc", "user:b"}, {1, "another", "user:a"}} {
		missing, err := repo.GetMetadata(ctx, key.tenant, key.service, key.principal)
		require.NoError(t, err)
		require.Nil(t, missing)
	}
	empty := *newer
	empty.SyncedAt = newer.SyncedAt.Add(time.Second)
	empty.Tools = []*types.MCPTool{}
	require.NoError(t, repo.SaveMetadata(ctx, &empty))
	got, err = repo.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	require.Empty(t, got.Tools, "successful empty directories retire removed tools")
}

func TestListMetadataSummariesCountsToolsWithoutReturningPayloads(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.MCPMetadata{})
	repo := &mcpServiceRepository{db: db}
	ctx := context.Background()
	require.NoError(t, repo.SaveMetadata(ctx, &types.MCPMetadata{
		TenantID:          1,
		ServiceID:         "svc",
		Principal:         "",
		ConfigFingerprint: "fp",
		SyncedAt:          time.Now().UTC(),
		Tools: []*types.MCPTool{
			{Name: "a", Description: "secret-sized description"},
			{Name: "b"},
		},
	}))
	require.NoError(t, repo.SaveMetadata(ctx, &types.MCPMetadata{
		TenantID:  1,
		ServiceID: "oauth",
		Principal: "user:a",
		SyncedAt:  time.Now().UTC(),
		Tools:     []*types.MCPTool{{Name: "only-a"}},
	}))
	rows, err := repo.ListMetadataSummaries(ctx, 1, []string{"", "user:a"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byID := map[string]*types.MCPMetadataSummary{}
	for _, row := range rows {
		byID[row.ServiceID] = row
	}
	require.Equal(t, 2, byID["svc"].ToolCount)
	require.Equal(t, 1, byID["oauth"].ToolCount)
	other, err := repo.ListMetadataSummaries(ctx, 1, []string{"user:b"})
	require.NoError(t, err)
	require.Empty(t, other)
}

// requireToolsEqual 按 JSON 语义比较工具目录。
//
// tools 落在 jsonb 列里，而 PostgreSQL 的 jsonb 会在写入时规范化文本：
// 键被重排、逗号和冒号后补上空格。所以同一份 JSON 在 PG 上读回来的字节
// 序列和写进去的不一样，字节级断言必然失败，只能按语义比较。
func requireToolsEqual(t *testing.T, want, got []*types.MCPTool) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].Name, got[i].Name)
		require.Equal(t, want[i].Description, got[i].Description)
		require.Equal(t, want[i].RequireApproval, got[i].RequireApproval)
		require.JSONEq(t, string(want[i].InputSchema), string(got[i].InputSchema))
	}
}
