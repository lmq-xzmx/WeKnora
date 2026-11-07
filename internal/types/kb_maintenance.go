package types

import "time"

// KB maintenance action identifiers.
//
// M0BackfillEmbeddings is the only action implemented today: it writes
// embeddings for chunks that already exist, without re-running the document
// pipeline. It never rebuilds chunks and never re-distills wiki pages, so
// wiki references to chunk ids stay valid.
const (
	KBMaintenanceActionBackfillEmbeddings = "backfill_embeddings"
)

// KBMaintenanceStage names the pipeline stage a maintenance run touches.
// M0 only touches "embedding".
const (
	KBMaintenanceStageEmbedding = "embedding"
)

// KBMaintenance run statuses.
const (
	KBMaintenanceStatusQueued  = "queued"
	KBMaintenanceStatusRunning = "running"
	KBMaintenanceStatusSuccess = "success"
	KBMaintenanceStatusFailed  = "failed"
)

// KBMaintenanceRun is one execution record of a maintenance action.
//
// The row is the audit trail: it records what was planned (target_chunks,
// affected_documents) versus what actually happened (chunks_done,
// embedding_calls, errors), plus the LLM cost counter. M0 must always
// report llm_calls = 0 — that invariant is what makes the action cheap
// compared with re-running the document pipeline.
type KBMaintenanceRun struct {
	ID                string    `json:"id"                gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64    `json:"tenant_id"         gorm:"not null"`
	KnowledgeBaseID   string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null"`
	Action            string    `json:"action"            gorm:"type:varchar(64);not null"`
	Stages            JSON      `json:"stages"            gorm:"type:json;not null"`
	Status            string    `json:"status"            gorm:"type:varchar(16);not null"`
	AffectedDocuments int       `json:"affected_documents"`
	TargetChunks      int       `json:"target_chunks"`
	ChunksDone        int       `json:"chunks_done"`
	EmbeddingCalls    int       `json:"embedding_calls"`
	LLMCalls          int       `json:"llm_calls"`
	Tokens            int64     `json:"tokens"`
	Errors            int       `json:"errors"`
	DryRun            bool      `json:"dry_run"`
	Message           string    `json:"message"`
	CreatedBy         string    `json:"created_by"        gorm:"type:varchar(64)"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TableName implements the GORM naming convention.
func (r *KBMaintenanceRun) TableName() string {
	return "kb_maintenance_runs"
}

// KBMaintenancePlanRequest asks for a cost estimate of a maintenance action.
// It is read-only: nothing is written and no embedding call is made.
type KBMaintenancePlanRequest struct {
	// Action selects the maintenance action. Only backfill_embeddings today.
	Action string `json:"action" binding:"required"`
}

// KBMaintenancePlanResponse is the cost estimate returned by the plan
// endpoint. Every number is computed from live data, never estimated by
// hand: target_chunks is a real count of indexable chunks, and the timing
// comes from the KB's own processing history.
type KBMaintenancePlanResponse struct {
	Action            string                     `json:"action"`
	Stages            []string                   `json:"stages"`
	AffectedDocuments int                        `json:"affected_documents"`
	TargetChunks      int                        `json:"target_chunks"`
	EmbeddingCallsEst int                        `json:"embedding_calls_est"`
	LLMCallsEst       int                        `json:"llm_calls_est"`
	LLMTokensEst      int64                      `json:"llm_tokens_est"`
	EstimatedMinutes  int                        `json:"estimated_minutes"`
	EmbeddingModel    *KBMaintenanceEmbeddingModel `json:"embedding_model"`
	// WikiImpact is "none" for M0: chunk ids do not change, so wiki pages
	// keep pointing at the same chunks.
	WikiImpact string   `json:"wiki_impact"`
	Warnings   []string `json:"warnings"`
}

// KBMaintenanceEmbeddingModel describes the embedding model the backfill
// will use.
type KBMaintenanceEmbeddingModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Dimension int    `json:"dimension"`
}

// KBMaintenanceRunRequest starts a maintenance run.
type KBMaintenanceRunRequest struct {
	// Action selects the maintenance action.
	Action string `json:"action" binding:"required"`
	// DryRun computes the plan and records it without writing any embedding.
	DryRun bool `json:"dry_run"`
	// BatchSize is the number of chunks embedded per request. 0 means the
	// service default.
	BatchSize int `json:"batch_size"`
}

// KBMaintenanceRunRequestResponse is returned when a run is accepted.
type KBMaintenanceRunRequestResponse struct {
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	TargetChunks int    `json:"target_chunks"`
}

// KBMaintenanceRepository persists maintenance runs.
//
// The interface itself lives in the interfaces package (see
// internal/types/interfaces/kb_maintenance.go) because that package already
// depends on this one — declaring it here would create an import cycle.

