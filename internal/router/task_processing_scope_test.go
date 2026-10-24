package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/testutil"
)

type transferFailureKnowledge struct {
	interfaces.KnowledgeService
	repo interfaces.KnowledgeRepository
}

func (s transferFailureKnowledge) GetRepository() interfaces.KnowledgeRepository { return s.repo }

func TestProcessingFailureCallbackDoesNotFollowMovedDocuments(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.Knowledge{})
	repo := repository.NewKnowledgeRepository(db)
	callback := newDeadLetterKnowledgeFailer(transferFailureKnowledge{repo: repo}, nil)
	for _, tc := range []struct {
		id, kb, status string
		tenant         uint64
		skip, changed  bool
	}{
		{"active", "source", types.ParseStatusProcessing, 7, false, true},
		{"moved", "target", types.ParseStatusProcessing, 7, false, false},
		{"foreign", "source", types.ParseStatusProcessing, 8, false, false},
		{"done", "source", types.ParseStatusCompleted, 7, false, false},
		{"rejected", "source", types.ParseStatusProcessing, 7, true, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			require.NoError(
				t,
				db.Create(
					&types.Knowledge{ID: tc.id, TenantID: tc.tenant, KnowledgeBaseID: tc.kb, ParseStatus: tc.status},
				).Error,
			)
			payload, err := json.Marshal(
				types.DocumentProcessPayload{TenantID: 7, KnowledgeBaseID: "source", KnowledgeID: tc.id},
			)
			require.NoError(t, err)
			failure := errors.New("timeout")
			if tc.skip {
				failure = asynq.SkipRetry
			}
			callback(context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload), failure)
			row, err := repo.GetKnowledgeByID(context.Background(), tc.tenant, tc.id)
			require.NoError(t, err)
			if tc.changed {
				require.Equal(t, types.ParseStatusFailed, row.ParseStatus)
			} else {
				require.Equal(t, tc.status, row.ParseStatus)
				require.Empty(t, row.ErrorMessage)
			}
		})
	}
}
