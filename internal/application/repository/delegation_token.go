package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrDelegationTokenNotFound = errors.New("delegation token not found")

// delegationTokenRepository implements interfaces.DelegationTokenRepository
type delegationTokenRepository struct {
	db *gorm.DB
}

// NewDelegationTokenRepository creates a new DelegationTokenRepository
func NewDelegationTokenRepository(db *gorm.DB) interfaces.DelegationTokenRepository {
	return &delegationTokenRepository{db: db}
}

// Create creates a new delegation token.
func (r *delegationTokenRepository) Create(ctx context.Context, token *types.DelegationToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// GetByTokenHash returns a token by its hash.
func (r *delegationTokenRepository) GetByTokenHash(ctx context.Context, hash string) (*types.DelegationToken, error) {
	var token types.DelegationToken
	if err := r.db.WithContext(ctx).
		Where("token_hash = ? AND (revoked_at IS NULL OR revoked_at > ?)", hash, time.Now()).
		First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

// ListByTenantID returns all tokens for a tenant.
func (r *delegationTokenRepository) ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.DelegationToken, error) {
	var tokens []*types.DelegationToken
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Find(&tokens).Error; err != nil {
		return nil, err
	}
	return tokens, nil
}

// Revoke marks a token as revoked.
func (r *delegationTokenRepository) Revoke(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).
		Model(&types.DelegationToken{}).
		Where("id = ?", id).
		UpdateColumn("revoked_at", time.Now()).Error
}

// Delete permanently deletes a token.
func (r *delegationTokenRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&types.DelegationToken{}, id).Error
}
