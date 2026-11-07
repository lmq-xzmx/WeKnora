package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// KBMaintenanceService exposes knowledge-base maintenance actions.
//
// M0 ("embedding-only backfill") is the only implemented action today: it
// writes embeddings for chunks that already exist, without re-running the
// document pipeline. No LLM call is involved, chunk ids stay stable, and
// wiki references therefore remain valid.
//
// The design principle behind every action here is that it may only ADD
// data (embedding rows, audit rows). Nothing existing is deleted or
// rewritten, so "roll back" is always a plain DELETE of what was added.
type KBMaintenanceService interface {
	// PlanBackfillEmbeddings computes the cost of a backfill without
	// changing anything: no embedding call, no database write.
	PlanBackfillEmbeddings(ctx context.Context, kbID string) (*types.KBMaintenancePlanResponse, error)

	// RunBackfillEmbeddings writes embeddings for the knowledge base's
	// existing chunks. A run with DryRun=true only records the plan.
	RunBackfillEmbeddings(
		ctx context.Context, kbID string, req *types.KBMaintenanceRunRequest,
	) (*types.KBMaintenanceRunRequestResponse, error)

	// ListRuns returns the recent maintenance runs of a knowledge base,
	// newest first.
	ListRuns(ctx context.Context, kbID string, limit int) ([]*types.KBMaintenanceRun, error)
}
