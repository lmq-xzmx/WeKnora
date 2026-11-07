package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// kbMaintenanceRepository implements interfaces.KBMaintenanceRepository.
type kbMaintenanceRepository struct {
	db *gorm.DB
}

// NewKBMaintenanceRepository constructs a GORM-backed implementation.
func NewKBMaintenanceRepository(db *gorm.DB) interfaces.KBMaintenanceRepository {
	return &kbMaintenanceRepository{db: db}
}

// Create inserts a run record. The ID is generated here when the caller
// left it empty: the column is UUID-typed, and GORM would send an empty
// string rather than falling back to the DB-side gen_random_uuid() default.
func (r *kbMaintenanceRepository) Create(ctx context.Context, run *types.KBMaintenanceRun) error {
	if run == nil {
		return errors.New("kb maintenance: nil run")
	}
	if run.KnowledgeBaseID == "" || run.Action == "" {
		return errors.New("kb maintenance: knowledge_base_id and action are required")
	}
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(run).Error
}

// Update persists progress changes. GORM's Save writes every column, which
// is what we want for a run row: the progress counters, the status and the
// message all move together.
func (r *kbMaintenanceRepository) Update(ctx context.Context, run *types.KBMaintenanceRun) error {
	if run == nil || run.ID == "" {
		return errors.New("kb maintenance: run id is required")
	}
	return r.db.WithContext(ctx).Save(run).Error
}

// GetByID returns one run of the given tenant.
func (r *kbMaintenanceRepository) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.KBMaintenanceRun, error) {
	if id == "" {
		return nil, errors.New("kb maintenance: run id is required")
	}
	var run types.KBMaintenanceRun
	err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&run).Error
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListByKnowledgeBase returns the most recent runs of a knowledge base,
// newest first.
func (r *kbMaintenanceRepository) ListByKnowledgeBase(
	ctx context.Context, tenantID uint64, kbID string, limit int,
) ([]*types.KBMaintenanceRun, error) {
	if kbID == "" {
		return nil, errors.New("kb maintenance: knowledge_base_id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	var runs []*types.KBMaintenanceRun
	err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND tenant_id = ?", kbID, tenantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&runs).Error
	if err != nil {
		return nil, err
	}
	return runs, nil
}
