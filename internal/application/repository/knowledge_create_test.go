package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeDefaultsCustomMetadataToEmptyObject(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.Knowledge{})

	repo := NewKnowledgeRepository(db)
	knowledge := &types.Knowledge{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		Title:           "document.txt",
		ParseStatus:     types.ParseStatusPending,
		EnableStatus:    "disabled",
	}

	require.NoError(t, repo.CreateKnowledge(context.Background(), knowledge))
	require.JSONEq(t, `{}`, string(knowledge.CustomMetadata))

	persisted, err := repo.GetKnowledgeByID(context.Background(), knowledge.TenantID, knowledge.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(persisted.CustomMetadata))
}
