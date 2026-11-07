package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// kbMaintKnowledgeRepo serves the knowledge list the maintenance service
// reads to obtain document titles.
type kbMaintKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledges []*types.Knowledge
}

func (r *kbMaintKnowledgeRepo) ListKnowledgeByKnowledgeBaseID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Knowledge, error) {
	return r.knowledges, nil
}

// kbMaintKBRepo serves a single knowledge base.
type kbMaintKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *kbMaintKBRepo) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

// kbMaintRunRepo records the audit rows the service writes.
type kbMaintRunRepo struct {
	interfaces.KBMaintenanceRepository
	runs []*types.KBMaintenanceRun
}

func (r *kbMaintRunRepo) Create(_ context.Context, run *types.KBMaintenanceRun) error {
	if run.ID == "" {
		run.ID = "run-1"
	}
	r.runs = append(r.runs, run)
	return nil
}

func (r *kbMaintRunRepo) Update(_ context.Context, run *types.KBMaintenanceRun) error {
	for i, existing := range r.runs {
		if existing.ID == run.ID {
			r.runs[i] = run
			return nil
		}
	}
	r.runs = append(r.runs, run)
	return nil
}

func (r *kbMaintRunRepo) GetByID(
	_ context.Context, _ uint64, _ string,
) (*types.KBMaintenanceRun, error) {
	return nil, nil
}

func (r *kbMaintRunRepo) ListByKnowledgeBase(
	_ context.Context, _ uint64, _ string, _ int,
) ([]*types.KBMaintenanceRun, error) {
	return r.runs, nil
}

// kbMaintModelService returns a fixed-dimension embedder so the plan
// endpoint can report the model without reaching an external service.
type kbMaintModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s *kbMaintModelService) GetEmbeddingModel(
	_ context.Context, _ string,
) (embedding.Embedder, error) {
	return s.embedder, nil
}

// kbMaintEmbedder is a deterministic embedder: it returns a fixed vector so
// tests never call a real model.
type kbMaintEmbedder struct {
	dimension int
}

func (e *kbMaintEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, e.dimension), nil
}

func (e *kbMaintEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for range texts {
		out = append(out, make([]float32, e.dimension))
	}
	return out, nil
}

func (e *kbMaintEmbedder) GetModelName() string { return "bge-m3@geo-434" }

func (e *kbMaintEmbedder) GetDimensions() int { return e.dimension }

func (e *kbMaintEmbedder) GetModelID() string { return "builtin-bge-m3" }

func (e *kbMaintEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(context.Background(), texts)
}

// TestKBMaintenancePlanCountsIndexableChunks verifies the plan endpoint's
// census against a real database: only chunk_type = "text" chunks count,
// parent-text chunks (context carriers) do not, and documents that are
// still being parsed are reported as skipped.
func TestKBMaintenancePlanCountsIndexableChunks(t *testing.T) {
	db := testutil.OpenTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.Knowledge{}))

	const (
		kbID     = "kb-1"
		tenantID = uint64(10000)
	)
	now := time.Now()

	// Two completed documents, one still finalizing.
	seed := []struct {
		id          string
		title       string
		parseStatus string
	}{
		{"k-1", "语文大师", "completed"},
		{"k-2", "百科", "completed"},
		{"k-3", "未完成", "finalizing"},
	}
	for _, doc := range seed {
		knowledge := &types.Knowledge{
			ID: doc.id, TenantID: tenantID, Title: doc.title, ParseStatus: doc.parseStatus,
			CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, db.Create(knowledge).Error)
	}

	// k-1: 2 text chunks; k-2: 1 text chunk + 1 parent_text chunk
	// (parent-text must NOT be counted); k-3: 1 text chunk whose document
	// is still being parsed.
	chunks := []types.Chunk{
		{ID: "c-1", TenantID: tenantID, KnowledgeID: "k-1", KnowledgeBaseID: kbID,
			Content: "一", ChunkType: types.ChunkTypeText, IsEnabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "c-2", TenantID: tenantID, KnowledgeID: "k-1", KnowledgeBaseID: kbID,
			Content: "二", ChunkType: types.ChunkTypeText, IsEnabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "c-3", TenantID: tenantID, KnowledgeID: "k-2", KnowledgeBaseID: kbID,
			Content: "三", ChunkType: types.ChunkTypeText, IsEnabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "c-4", TenantID: tenantID, KnowledgeID: "k-2", KnowledgeBaseID: kbID,
			Content: "父块", ChunkType: types.ChunkTypeParentText, IsEnabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "c-5", TenantID: tenantID, KnowledgeID: "k-3", KnowledgeBaseID: kbID,
			Content: "五", ChunkType: types.ChunkTypeText, IsEnabled: true, CreatedAt: now, UpdatedAt: now},
	}
	for i := range chunks {
		require.NoError(t, db.Create(&chunks[i]).Error)
	}

	kb := &types.KnowledgeBase{
		ID: kbID, TenantID: tenantID, Type: types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "builtin-bge-m3",
	}
	kb.IndexingStrategy = types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true}

	svc := &kbMaintenanceService{
		kbRepo: &kbMaintKBRepo{kb: kb},
		kgRepo: &kbMaintKnowledgeRepo{knowledges: []*types.Knowledge{
			{ID: "k-1", Title: "语文大师"},
			{ID: "k-2", Title: "百科"},
			{ID: "k-3", Title: "未完成"},
		}},
		modelService:    &kbMaintModelService{embedder: &kbMaintEmbedder{dimension: 1024}},
		maintenanceRepo: &kbMaintRunRepo{},
		db:              db,
	}

	plan, err := svc.collectBackfillStats(context.Background(), kb)
	require.NoError(t, err)

	// 4 text chunks total; the parent_text chunk is excluded.
	require.Equal(t, 4, plan.targetChunks)
	// 3 documents own at least one text chunk.
	require.Equal(t, 3, plan.documents)
	// k-3 is still finalizing → skipped.
	require.Equal(t, 1, plan.skippedDocuments)
}

// TestKBMaintenancePlanWarnsWhenVectorRetrievalDisabled pins the warning
// that matters most in practice: backfilling vectors while the knowledge
// base has vector retrieval switched off produces rows nothing reads.
func TestKBMaintenancePlanWarnsWhenVectorRetrievalDisabled(t *testing.T) {
	db := testutil.OpenTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.Knowledge{}))

	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 10000, Type: types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "builtin-bge-m3",
	}
	kb.IndexingStrategy = types.IndexingStrategy{VectorEnabled: false, KeywordEnabled: false}

	svc := &kbMaintenanceService{
		kbRepo:          &kbMaintKBRepo{kb: kb},
		modelService:    &kbMaintModelService{embedder: &kbMaintEmbedder{dimension: 1024}},
		maintenanceRepo: &kbMaintRunRepo{},
		db:              db,
	}

	plan, err := svc.PlanBackfillEmbeddings(context.Background(), kb.ID)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Warnings)
	require.Contains(t, joinWarnings(plan.Warnings), "vector_enabled=false")
	require.Contains(t, joinWarnings(plan.Warnings), "keyword_enabled=false")
	// M0 never calls an LLM — that is the whole point of the action.
	require.Equal(t, 0, plan.LLMCallsEst)
	require.Equal(t, int64(0), plan.LLMTokensEst)
	require.Equal(t, 1024, plan.EmbeddingModel.Dimension)
}

// TestKBMaintenanceDryRunWritesNoEmbeddings verifies that a dry run records
// the audit trail but touches nothing else.
func TestKBMaintenanceDryRunWritesNoEmbeddings(t *testing.T) {
	db := testutil.OpenTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.Knowledge{}))

	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 10000, Type: types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "builtin-bge-m3",
	}
	kb.IndexingStrategy = types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true}

	runRepo := &kbMaintRunRepo{}
	svc := &kbMaintenanceService{
		kbRepo:          &kbMaintKBRepo{kb: kb},
		modelService:    &kbMaintModelService{embedder: &kbMaintEmbedder{dimension: 1024}},
		maintenanceRepo: runRepo,
		db:              db,
	}

	resp, err := svc.RunBackfillEmbeddings(context.Background(), kb.ID, &types.KBMaintenanceRunRequest{
		Action: types.KBMaintenanceActionBackfillEmbeddings,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Equal(t, "run-1", resp.RunID)
	require.Len(t, runRepo.runs, 1)
	require.Equal(t, types.KBMaintenanceStatusSuccess, runRepo.runs[0].Status)
	require.True(t, runRepo.runs[0].DryRun)
	require.Equal(t, 0, runRepo.runs[0].ChunksDone)
	require.Equal(t, 0, runRepo.runs[0].EmbeddingCalls)
	require.Equal(t, 0, runRepo.runs[0].LLMCalls)
}

// TestKBMaintenanceRunWithoutEmbeddingModelFails verifies the guard: a
// knowledge base without a bound embedding model cannot be backfilled, and
// the attempt is refused before anything is written.
func TestKBMaintenanceRunWithoutEmbeddingModelFails(t *testing.T) {
	db := testutil.OpenTestDB(t)

	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 10000, Type: types.KnowledgeBaseTypeDocument}
	kb.IndexingStrategy = types.IndexingStrategy{VectorEnabled: true}

	svc := &kbMaintenanceService{
		kbRepo:          &kbMaintKBRepo{kb: kb},
		maintenanceRepo: &kbMaintRunRepo{},
		db:              db,
	}

	_, err := svc.RunBackfillEmbeddings(context.Background(), kb.ID, &types.KBMaintenanceRunRequest{
		Action: types.KBMaintenanceActionBackfillEmbeddings,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no embedding model")
}

func joinWarnings(warnings []string) string {
	out := ""
	for _, w := range warnings {
		out += w + "\n"
	}
	return out
}
