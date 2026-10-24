package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTransferCheckpointReturnsStoredTimestampPrecision(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	// Emulate a database that stores fewer fractional digits than time.Now.
	// PostgreSQL 的 timestamptz 精度是微秒，比 SQLite 的秒级更细，所以这里
	// 用一个 BEFORE UPDATE 触发器把 updated_at 截断到秒，模拟精度更低的存储。
	require.NoError(t, db.Exec(`CREATE OR REPLACE FUNCTION truncate_checkpoint_time() RETURNS trigger AS $$
	BEGIN
		NEW.updated_at := date_trunc('second', NEW.updated_at);
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql`).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER truncate_checkpoint_time BEFORE UPDATE ON knowledges
		FOR EACH ROW EXECUTE FUNCTION truncate_checkpoint_time()`).Error)
	row := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "source", ParseStatus: types.ParseStatusCompleted}
	require.NoError(t, db.Omit("custom_metadata").Create(row).Error)
	repo := NewKnowledgeRepository(db)
	before, err := repo.GetKnowledgeByID(context.Background(), 7, "doc")
	require.NoError(t, err)
	after := *before
	after.ParseStatus = types.ParseStatusProcessing
	require.NoError(t, repo.UpdateKnowledgeForTransfer(context.Background(), before, &after))
	require.Zero(t, after.UpdatedAt.Nanosecond())
	final := after
	final.KnowledgeBaseID = "target"
	final.ParseStatus = types.ParseStatusCompleted
	require.NoError(t, repo.UpdateKnowledgeForTransfer(context.Background(), &after, &final))
}
