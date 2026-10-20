package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrTenantDomainNotFound = errors.New("tenant domain not found")

// tenantDomainRepository implements interfaces.TenantDomainRepository
type tenantDomainRepository struct {
	db *gorm.DB
}

// NewTenantDomainRepository creates a new TenantDomainRepository
func NewTenantDomainRepository(db *gorm.DB) interfaces.TenantDomainRepository {
	return &tenantDomainRepository{db: db}
}

// GetByDomain returns the tenant domain mapping for the given domain.
// Returns nil if not found.
func (r *tenantDomainRepository) GetByDomain(ctx context.Context, domain string) (*types.TenantDomain, error) {
	var td types.TenantDomain
	if err := r.db.WithContext(ctx).
		Where("domain = ? AND is_active = ?", domain, true).
		First(&td).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &td, nil
}

// Create creates a new tenant domain mapping.
func (r *tenantDomainRepository) Create(ctx context.Context, td *types.TenantDomain) error {
	return r.db.WithContext(ctx).Create(td).Error
}

// Delete deletes a tenant domain mapping.
func (r *tenantDomainRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&types.TenantDomain{}, id).Error
}

// ListByTenantID lists all domain mappings for a tenant.
func (r *tenantDomainRepository) ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.TenantDomain, error) {
	var domains []*types.TenantDomain
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&domains).Error; err != nil {
		return nil, err
	}
	return domains, nil
}
