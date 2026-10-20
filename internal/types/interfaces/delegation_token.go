package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// DelegationTokenRepository defines data access for delegation tokens.
type DelegationTokenRepository interface {
	// Create creates a new delegation token.
	Create(ctx context.Context, token *types.DelegationToken) error
	// GetByTokenHash returns a token by its hash. Returns nil if not found.
	GetByTokenHash(ctx context.Context, hash string) (*types.DelegationToken, error)
	// ListByTenantID returns all tokens for a tenant.
	ListByTenantID(ctx context.Context, tenantID uint64) ([]*types.DelegationToken, error)
	// Revoke marks a token as revoked.
	Revoke(ctx context.Context, id uint64) error
	// Delete permanently deletes a token.
	Delete(ctx context.Context, id uint64) error
}

// DelegationTokenService defines business logic for delegation tokens.
type DelegationTokenService interface {
	// CreateToken creates a new delegation token and returns the plain token (only time it's returned).
	CreateToken(ctx context.Context, req DelegationTokenCreateRequest) (*DelegationTokenCreateResult, error)
	// ValidateToken validates a plain token and returns the resolved token record.
	ValidateToken(ctx context.Context, plainToken string) (*types.DelegationToken, error)
	// ListTokens returns all tokens for a tenant.
	ListTokens(ctx context.Context, tenantID uint64) ([]*types.DelegationToken, error)
	// RevokeToken revokes a token by ID.
	RevokeToken(ctx context.Context, id uint64) error
	// DeleteToken permanently deletes a token.
	DeleteToken(ctx context.Context, id uint64) error
}

// DelegationTokenCreateRequest is the input for CreateToken.
type DelegationTokenCreateRequest struct {
	TenantID   uint64
	Name      string
	Scopes    []string
	AllowedIPs []string
	ExpiresIn string // e.g. "1h", "7d", "30d"
	CreatedBy string
}

// DelegationTokenCreateResult is the output of CreateToken.
// The plain token is only returned at creation time.
type DelegationTokenCreateResult struct {
	Token *types.DelegationToken
	Plain string
}
