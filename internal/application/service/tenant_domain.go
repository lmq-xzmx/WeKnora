package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// tenantDomainService implements interfaces.TenantDomainService
type tenantDomainService struct {
	domainRepo interfaces.TenantDomainRepository
	tenantSvc  interfaces.TenantService
}

// NewTenantDomainService creates a new TenantDomainService instance
func NewTenantDomainService(
	domainRepo interfaces.TenantDomainRepository,
	tenantSvc interfaces.TenantService,
) interfaces.TenantDomainService {
	return &tenantDomainService{
		domainRepo: domainRepo,
		tenantSvc:  tenantSvc,
	}
}

// GetTenantByDomain returns the tenant for the given domain.
// Returns nil if no domain mapping exists (caller should fall back to default/global tenant).
func (s *tenantDomainService) GetTenantByDomain(ctx context.Context, domain string) (*types.Tenant, error) {
	td, err := s.domainRepo.GetByDomain(ctx, domain)
	if err != nil || td == nil {
		return nil, nil // not found → fallback to default
	}
	return s.tenantSvc.GetTenantByID(ctx, td.TenantID)
}

// GetOIDCConfigByDomain returns the OIDC config for the given domain.
// Returns nil if no tenant-specific OIDC config is found (caller should use global config).
func (s *tenantDomainService) GetOIDCConfigByDomain(ctx context.Context, domain string) (*types.TenantOIDCConfig, error) {
	td, err := s.domainRepo.GetByDomain(ctx, domain)
	if err != nil || td == nil {
		return nil, nil // not found → fallback to global config
	}
	tenant, err := s.tenantSvc.GetTenantByID(ctx, td.TenantID)
	if err != nil || tenant == nil {
		return nil, nil
	}
	return tenant.OIDCConfig, nil
}

// Create creates a new tenant domain mapping.
func (s *tenantDomainService) Create(ctx context.Context, td *types.TenantDomain) error {
	return s.domainRepo.Create(ctx, td)
}

// Delete deletes a tenant domain mapping by ID.
func (s *tenantDomainService) Delete(ctx context.Context, id uint64) error {
	return s.domainRepo.Delete(ctx, id)
}

// ListByTenantID lists all domain mappings for a tenant.
func (s *tenantDomainService) ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.TenantDomain, error) {
	return s.domainRepo.ListByTenantID(ctx, tenantID)
}
