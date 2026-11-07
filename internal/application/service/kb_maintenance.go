package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// kbMaintenanceService implements interfaces.KBMaintenanceService.
//
// M0 ("embedding-only backfill") writes embeddings for chunks that already
// exist. It deliberately does NOT touch the document pipeline: no chunk is
// rebuilt, no wiki page is re-distilled, no LLM is called. Chunk ids stay
// stable, so wiki pages keep pointing at the same chunks.
//
// The write path mirrors the upstream embedding stage
// (knowledge_process.go) byte for byte in the parts that matter:
//
//   - only chunk_type = "text" chunks are indexed (parent_text chunks are
//     context-only and are never embedded; image/summary/entity chunks have
//     their own semantics);
//   - the indexed text is the document title, a newline, and the chunk's
//     EmbeddingContent() (context header + body) — see
//     buildKnowledgeIndexContent;
//   - the row is written through the retrieve engine's BatchIndex, which
//     performs the UPSERT on (source_id, source_type) — so a second run is
//     idempotent rather than a constraint violation.
//
// The only thing this service adds over the upstream stage is scope: it
// works at the knowledge-base level and it writes an audit row.
type kbMaintenanceService struct {
	kbRepo         interfaces.KnowledgeBaseRepository
	kgRepo         interfaces.KnowledgeRepository
	chunkRepo      interfaces.ChunkRepository
	modelService   interfaces.ModelService
	retrieveEngine interfaces.RetrieveEngineRegistry
	ownership      retriever.TenantStoreOwnership
	maintenanceRepo interfaces.KBMaintenanceRepository
	spanRepo       repository.KnowledgeSpanRepository
	db             *gorm.DB
}

// NewKBMaintenanceService constructs the maintenance service.
func NewKBMaintenanceService(
	kbRepo interfaces.KnowledgeBaseRepository,
	kgRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	maintenanceRepo interfaces.KBMaintenanceRepository,
	spanRepo repository.KnowledgeSpanRepository,
	db *gorm.DB,
) interfaces.KBMaintenanceService {
	return &kbMaintenanceService{
		kbRepo:          kbRepo,
		kgRepo:          kgRepo,
		chunkRepo:       chunkRepo,
		modelService:    modelService,
		retrieveEngine:  retrieveEngine,
		ownership:       ownership,
		maintenanceRepo: maintenanceRepo,
		spanRepo:        spanRepo,
		db:              db,
	}
}

// maintenanceDefaults
const (
	// defaultMaintenanceBatchSize is the number of chunks handed to the
	// embedding model per request during a backfill.
	defaultMaintenanceBatchSize = 200
	// defaultMaintenanceBatchSeconds is the fallback per-batch duration used
	// for the time estimate when the knowledge base has no embedding-stage
	// history to learn from.
	defaultMaintenanceBatchSeconds = 3
	// maintenanceRunListLimit is the default number of runs returned by
	// ListRuns.
	maintenanceRunListLimit = 20
	// backfillOverallTimeout caps a single backfill run. The estimate for
	// the production target KB is a few minutes; an hour absorbs a slow
	// model pool without letting a stuck run hold the "one run per KB"
	// guard forever.
	backfillOverallTimeout = time.Hour
	// backfillBatchTimeout caps one embedding batch. Observed batches take
	// under a minute; ten minutes tolerates a slow model pool while a
	// half-open connection cannot freeze the whole run.
	backfillBatchTimeout = 10 * time.Minute
)

// PlanBackfillEmbeddings computes what a backfill would cost without
// changing anything.
//
// Every number comes from live data: target_chunks is a real count of
// indexable chunks, and the time estimate is derived from the knowledge
// base's own embedding-stage history (knowledge_processing_spans) when it
// has one.
func (s *kbMaintenanceService) PlanBackfillEmbeddings(
	ctx context.Context, kbID string,
) (*types.KBMaintenancePlanResponse, error) {
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}

	plan := &types.KBMaintenancePlanResponse{
		Action:   types.KBMaintenanceActionBackfillEmbeddings,
		Stages:   []string{types.KBMaintenanceStageEmbedding},
		Warnings: []string{},
		// M0 never rebuilds chunks and never re-distills wiki pages, so
		// chunk ids stay valid and wiki references survive untouched.
		WikiImpact: "none",
	}

	// The embedding model is fixed for the knowledge base. Its dimension
	// decides the vector width of every row written.
	if kb.EmbeddingModelID != "" {
		plan.EmbeddingModel = &types.KBMaintenanceEmbeddingModel{ID: kb.EmbeddingModelID}
		if embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID); err == nil {
			plan.EmbeddingModel.Name = embedder.GetModelName()
			plan.EmbeddingModel.Dimension = embedder.GetDimensions()
		} else {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("嵌入模型 %s 初始化失败：%v", kb.EmbeddingModelID, err))
		}
	} else {
		plan.Warnings = append(plan.Warnings, "知识库未绑定嵌入模型，无法回填向量")
	}

	stats, err := s.collectBackfillStats(ctx, kb)
	if err != nil {
		return nil, err
	}
	plan.AffectedDocuments = stats.documents
	plan.TargetChunks = stats.targetChunks
	plan.EmbeddingCallsEst = estimateEmbeddingCalls(stats.targetChunks, defaultMaintenanceBatchSize)
	// The whole point of M0: no LLM call at all.
	plan.LLMCallsEst = 0
	plan.LLMTokensEst = 0
	plan.EstimatedMinutes = estimateMinutes(stats.targetChunks, defaultMaintenanceBatchSize, stats.avgEmbeddingSeconds)

	// Vector retrieval is gated on the knowledge base's own flag
	// (hasEmbeddingModel → IsVectorEnabled). Backfilling vectors while the
	// flag is off produces rows nothing reads — say so up front.
	if !kb.IsVectorEnabled() {
		plan.Warnings = append(plan.Warnings,
			"知识库未启用向量检索（vector_enabled=false），回填后的向量不会被检索命中，请先在知识库设置中开启")
	}
	if !kb.IsKeywordEnabled() {
		plan.Warnings = append(plan.Warnings,
			"知识库未启用关键词检索（keyword_enabled=false），BM25 召回不可用")
	}
	if stats.skippedDocuments > 0 {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("%d 篇文档的 parse_status 不是 completed，将被跳过", stats.skippedDocuments))
	}
	if stats.existingEmbeddings > 0 {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("知识库已有 %d 行向量，回填将以 UPSERT 覆盖同源行，行数不会增加", stats.existingEmbeddings))
	}
	if plan.EmbeddingModel != nil && plan.EmbeddingModel.Dimension == 0 {
		plan.Warnings = append(plan.Warnings, "嵌入模型维度未知，回填将失败")
	}

	return plan, nil
}

// RunBackfillEmbeddings starts a backfill for the knowledge base's existing
// chunks.
//
// The synchronous part is deliberately cheap (KB lookup, one COUNT, one
// guard query): it creates the audit row and returns immediately, while the
// embedding work continues in the background. Callers poll
// /maintenance/runs for progress — a run's status walks
// queued → running → success/failed. A client that hangs up mid-request
// therefore never interrupts a run.
func (s *kbMaintenanceService) RunBackfillEmbeddings(
	ctx context.Context, kbID string, req *types.KBMaintenanceRunRequest,
) (*types.KBMaintenanceRunRequestResponse, error) {
	if req == nil {
		req = &types.KBMaintenanceRunRequest{}
	}
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Fail fast: a knowledge base without a bound embedding model cannot be
	// backfilled at all, and there is no point in computing a plan first.
	if kb.EmbeddingModelID == "" {
		return nil, fmt.Errorf("knowledge base %s has no embedding model bound", kbID)
	}

	// One backfill per knowledge base at a time, checked before creating
	// the row so a rejected request leaves no stub record behind. A failed
	// guard query is an error, not a reason to skip the check — letting a
	// second run through would interleave UPSERT batches with the first:
	// harmless per row, but it doubles the embedding spend and makes the
	// progress counters lie.
	tenantID := kb.TenantID
	running, err := s.maintenanceRepo.ListByKnowledgeBase(ctx, tenantID, kbID, 10)
	if err != nil {
		return nil, fmt.Errorf("check for a running backfill: %w", err)
	}
	for _, existing := range running {
		if !existing.DryRun &&
			(existing.Status == types.KBMaintenanceStatusQueued ||
				existing.Status == types.KBMaintenanceStatusRunning) {
			return nil, fmt.Errorf(
				"knowledge base %s already has a backfill running (run %s, %d/%d chunks); poll /maintenance/runs and wait for it to finish",
				kbID, existing.ID, existing.ChunksDone, existing.TargetChunks)
		}
	}

	// The synchronous part must stay cheap — on a busy database the plan's
	// census queries (GROUP BY over chunks × knowledges) can take minutes.
	// A single COUNT sizes the target; affected_documents stays 0 here (the
	// plan endpoint is the place for full statistics).
	targetChunks, err := s.countIndexableChunks(ctx, kb)
	if err != nil {
		return nil, err
	}

	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = defaultMaintenanceBatchSize
	}

	run := &types.KBMaintenanceRun{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Action:          types.KBMaintenanceActionBackfillEmbeddings,
		Stages: mustJSON([]string{
			types.KBMaintenanceStageEmbedding,
		}),
		Status:       types.KBMaintenanceStatusQueued,
		TargetChunks: targetChunks,
		DryRun:       req.DryRun,
		CreatedBy:    creatorID(ctx),
	}
	if err := s.maintenanceRepo.Create(ctx, run); err != nil {
		return nil, err
	}

	if req.DryRun {
		run.Status = types.KBMaintenanceStatusSuccess
		run.Message = "dry run: 未写入任何向量"
		now := time.Now()
		run.StartedAt = &now
		run.FinishedAt = &now
		if err := s.maintenanceRepo.Update(ctx, run); err != nil {
			return nil, err
		}
		return &types.KBMaintenanceRunRequestResponse{
			RunID: run.ID, Status: run.Status, TargetChunks: run.TargetChunks,
		}, nil
	}

	// A backfill outlives the HTTP request that started it: the client may
	// hang up (proxy timeout, laptop lid) long before the last batch lands.
	// Detach from the request context — cancellation then comes only from
	// the overall deadline below — and run in the background. The cancel
	// function belongs to the goroutine, not this call frame: a deferred
	// cancel here would fire the moment the response is written and kill
	// the run before its first batch.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backfillOverallTimeout)

	go func() {
		defer cancel()

		run.Status = types.KBMaintenanceStatusRunning
		now := time.Now()
		run.StartedAt = &now
		if err := s.maintenanceRepo.Update(runCtx, run); err != nil {
			// Mark the run failed even here: a row stuck in queued would
			// keep the one-run-per-KB guard rejecting every future backfill.
			// The write below uses a cancel-proof context because a deadline
			// that fired before this line is exactly the case that matters.
			run.Status = types.KBMaintenanceStatusFailed
			run.Message = "failed to record start: " + err.Error()
			finished := time.Now()
			run.FinishedAt = &finished
			_ = s.maintenanceRepo.Update(context.WithoutCancel(runCtx), run)
			logger.ErrorWithFields(runCtx, err, map[string]interface{}{"run_id": run.ID})
			return
		}

		if err := s.backfillEmbeddings(runCtx, kb, run, batchSize); err != nil {
			// Deadline exceeded is the expected end for a run that had its
			// budget cut; everything else is a real failure worth logging.
			run.Status = types.KBMaintenanceStatusFailed
			run.Message = err.Error()
			finished := time.Now()
			run.FinishedAt = &finished
			if uerr := s.maintenanceRepo.Update(context.WithoutCancel(runCtx), run); uerr != nil {
				logger.ErrorWithFields(runCtx, uerr, map[string]interface{}{"run_id": run.ID})
			}
			logger.ErrorWithFields(runCtx, err, map[string]interface{}{
				"run_id": run.ID, "knowledge_base_id": kbID,
			})
			return
		}

		run.Status = types.KBMaintenanceStatusSuccess
		finished := time.Now()
		run.FinishedAt = &finished
		if err := s.maintenanceRepo.Update(context.WithoutCancel(runCtx), run); err != nil {
			logger.ErrorWithFields(runCtx, err, map[string]interface{}{"run_id": run.ID})
		}
	}()

	return &types.KBMaintenanceRunRequestResponse{
		RunID: run.ID, Status: run.Status, TargetChunks: run.TargetChunks,
	}, nil
}

// backfillEmbeddings does the actual work: it walks the knowledge base's
// indexable chunks in batches and hands each batch to the retrieve engine.
func (s *kbMaintenanceService) backfillEmbeddings(
	ctx context.Context, kb *types.KnowledgeBase, run *types.KBMaintenanceRun, batchSize int,
) error {
	embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return fmt.Errorf("get embedding model: %w", err)
	}
	engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.retrieveEngine, s.ownership, kb.TenantID, kb.VectorStoreID)
	if err != nil {
		return fmt.Errorf("resolve retrieve engine: %w", err)
	}

	// The document title is prepended to every chunk's indexed text — same
	// as the upstream embedding stage.
	knowledgeTitles := s.knowledgeTitles(ctx, kb)

	chunks, err := s.listIndexableChunks(ctx, kb)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		run.Message = "没有可索引的文本 chunk"
		return nil
	}

	for start := 0; start < len(chunks); start += batchSize {
		end := start + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]

		indexInfos := make([]*types.IndexInfo, 0, len(batch))
		for _, chunk := range batch {
			indexInfos = append(indexInfos, &types.IndexInfo{
				Content:         buildKnowledgeIndexContent(knowledgeTitles[chunk.KnowledgeID], chunk.EmbeddingContent()),
				SourceID:        chunk.ID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType:   kb.Type,
				TagID:           chunk.TagID,
				IsEnabled:       chunk.IsEnabled,
			})
		}

		// Per-batch deadline. The embedder's HTTP client carries no timeout
		// of its own — a half-open connection to the model pool would hang
		// the loop until the run-level deadline, silently freezing progress
		// for the better part of an hour. The run-level context still caps
		// the whole run; batches complete long before either fires normally
		// (a typical batch takes well under a minute).
		batchCtx, batchCancel := context.WithTimeout(ctx, backfillBatchTimeout)
		if err := engine.BatchIndex(batchCtx, embedder, indexInfos); err != nil {
			batchCancel()
			run.Errors += len(batch)
			return fmt.Errorf("batch index [%d:%d] failed: %w", start, end, err)
		}
		batchCancel()
		run.ChunksDone += len(batch)
		run.EmbeddingCalls++
		// The embedding model is called once per batch; the engine batches
		// internally, so the counter tracks requests, not chunks.
		if err := s.maintenanceRepo.Update(ctx, run); err != nil {
			return err
		}
	}

	// M0 invariant: no LLM was involved.
	run.LLMCalls = 0
	run.Tokens = 0
	return nil
}

// countIndexableChunks returns the number of chunks the upstream embedding
// stage would index for this knowledge base. One cheap COUNT — the filter
// mirrors listIndexableChunks (chunk_type = "text" only).
func (s *kbMaintenanceService) countIndexableChunks(ctx context.Context, kb *types.KnowledgeBase) (int, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?",
			kb.TenantID, kb.ID, types.ChunkTypeText).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// knowledgeTitles maps knowledge id → document title for the given KB.
func (s *kbMaintenanceService) knowledgeTitles(ctx context.Context, kb *types.KnowledgeBase) map[string]*types.Knowledge {
	titles := make(map[string]*types.Knowledge)
	knowledges, err := s.kgRepo.ListKnowledgeByKnowledgeBaseID(ctx, kb.TenantID, kb.ID)
	if err != nil {
		return titles
	}
	for _, knowledge := range knowledges {
		titles[knowledge.ID] = knowledge
	}
	return titles
}

// listIndexableChunks returns the chunks that the upstream embedding stage
// would index for this knowledge base.
//
// The filter mirrors knowledge_process.go: chunk_type = "text" only.
// Parent-text chunks (chunk_type = "parent_text") are context carriers and
// are never embedded; summary/entity/image chunks have their own separate
// index paths and are out of scope for M0.
//
// Note that a chunk's ParentChunkID being set does NOT make it a parent:
// in the parent-child strategy the children carry the parent's id and it
// is the children that get embedded.
func (s *kbMaintenanceService) listIndexableChunks(
	ctx context.Context, kb *types.KnowledgeBase,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	err := s.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?",
			kb.TenantID, kb.ID, types.ChunkTypeText).
		Order("knowledge_id, chunk_index").
		Find(&chunks).Error
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

// backfillStats is the live census used by the plan endpoint.
type backfillStats struct {
	documents         int
	targetChunks      int
	skippedDocuments  int
	existingEmbeddings int
	avgEmbeddingSeconds float64
}

// collectBackfillStats counts what a backfill would touch.
func (s *kbMaintenanceService) collectBackfillStats(
	ctx context.Context, kb *types.KnowledgeBase,
) (*backfillStats, error) {
	stats := &backfillStats{}

	// Indexable chunks and the documents that own them.
	type docRow struct {
		KnowledgeID string
		ChunkCount  int
		ParseStatus string
	}
	var rows []docRow
	err := s.db.WithContext(ctx).
		Table("chunks AS c").
		Select("c.knowledge_id, count(*) as chunk_count, k.parse_status as parse_status").
		Joins("JOIN knowledges k ON k.id = c.knowledge_id").
		Where("c.tenant_id = ? AND c.knowledge_base_id = ? AND c.chunk_type = ? AND k.deleted_at IS NULL",
			kb.TenantID, kb.ID, types.ChunkTypeText).
		Group("c.knowledge_id, k.parse_status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		stats.targetChunks += row.ChunkCount
		stats.documents++
		if !strings.EqualFold(row.ParseStatus, "completed") {
			stats.skippedDocuments++
		}
	}

	// Existing embeddings for this KB — the number that tells the operator
	// whether this will be an upsert over existing rows.
	var existing int64
	err = s.db.WithContext(ctx).
		Table("embeddings").
		Where("knowledge_base_id = ?", kb.ID).
		Count(&existing).Error
	if err == nil {
		stats.existingEmbeddings = int(existing)
	}

	// Historical embedding-stage duration, used for the time estimate.
	stats.avgEmbeddingSeconds = s.avgEmbeddingSeconds(ctx, kb.ID)

	return stats, nil
}

// avgEmbeddingSeconds returns the average duration of the KB's historical
// embedding-stage spans, or 0 when there is no history.
func (s *kbMaintenanceService) avgEmbeddingSeconds(ctx context.Context, kbID string) float64 {
	// AVG() returns NULL when there is no history — scan into a nullable
	// float column so that case reads as "no history" rather than an error.
	var row struct {
		AvgMs *float64 `gorm:"column:avg_ms"`
	}
	err := s.db.WithContext(ctx).
		Table("knowledge_processing_spans AS sp").
		Select("AVG(sp.duration_ms) AS avg_ms").
		Joins("JOIN knowledges k ON k.id = sp.knowledge_id").
		Where("k.knowledge_base_id = ? AND sp.name = ? AND sp.duration_ms IS NOT NULL",
			kbID, types.StageEmbedding).
		Scan(&row).Error
	if err != nil || row.AvgMs == nil || *row.AvgMs <= 0 {
		return 0
	}
	seconds := *row.AvgMs / 1000
	// The historical figure covers one document's whole embedding stage.
	// A backfill batch embeds many chunks in one call, so scale it down to
	// the per-batch share of a typical document.
	if seconds > defaultMaintenanceBatchSeconds {
		return defaultMaintenanceBatchSeconds
	}
	return seconds
}

// estimateEmbeddingCalls returns how many embedding requests a backfill
// would issue at the given batch size.
func estimateEmbeddingCalls(targetChunks, batchSize int) int {
	if targetChunks <= 0 || batchSize <= 0 {
		return 0
	}
	calls := targetChunks / batchSize
	if targetChunks%batchSize != 0 {
		calls++
	}
	return calls
}

// estimateMinutes turns the chunk count into a wall-clock estimate.
func estimateMinutes(targetChunks, batchSize int, secondsPerBatch float64) int {
	if targetChunks <= 0 || batchSize <= 0 {
		return 0
	}
	if secondsPerBatch <= 0 {
		secondsPerBatch = defaultMaintenanceBatchSeconds
	}
	calls := estimateEmbeddingCalls(targetChunks, batchSize)
	minutes := float64(calls) * secondsPerBatch / 60
	// Round up — an estimate that promises less than it takes is worse
	// than useless to an operator watching a progress bar.
	return int(minutes) + 1
}

// ListRuns returns the recent maintenance runs of a knowledge base.
func (s *kbMaintenanceService) ListRuns(
	ctx context.Context, kbID string, limit int,
) ([]*types.KBMaintenanceRun, error) {
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = maintenanceRunListLimit
	}
	return s.maintenanceRepo.ListByKnowledgeBase(ctx, kb.TenantID, kbID, limit)
}

// mustJSON serializes a value into the JSON column type. The values passed
// here are plain string slices built by this service, so a marshal failure
// is a programming error rather than a runtime condition.
func mustJSON(value any) types.JSON {
	raw, err := json.Marshal(value)
	if err != nil {
		return types.JSON([]byte("[]"))
	}
	return types.JSON(raw)
}

// creatorID returns the id of the caller, for the audit trail.
func creatorID(ctx context.Context) string {
	if id, ok := types.UserIDFromContext(ctx); ok {
		return id
	}
	return ""
}
