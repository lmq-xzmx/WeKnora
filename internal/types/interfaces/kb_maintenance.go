package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KBMaintenanceRepository persists knowledge-base maintenance runs.
//
// Runs are the audit trail of a maintenance action: what was planned
// (target_chunks) versus what happened (chunks_done, embedding_calls,
// llm_calls). All lookups are tenant-scoped — a run is only visible to
// the tenant that owns the knowledge base.
type KBMaintenanceRepository interface {
	// Create inserts a new run record.
	Create(ctx context.Context, run *types.KBMaintenanceRun) error
	// Update persists progress changes of an existing run.
	Update(ctx context.Context, run *types.KBMaintenanceRun) error
	// GetByID returns one run of the given tenant.
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.KBMaintenanceRun, error)
	// ListByKnowledgeBase returns the most recent runs of a knowledge base,
	// newest first. limit <= 0 falls back to the repository default.
	ListByKnowledgeBase(ctx context.Context, tenantID uint64, kbID string, limit int) ([]*types.KBMaintenanceRun, error)
}
