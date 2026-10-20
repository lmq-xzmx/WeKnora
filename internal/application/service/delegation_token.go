package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// delegationTokenService implements interfaces.DelegationTokenService
type delegationTokenService struct {
	repo interfaces.DelegationTokenRepository
}

// NewDelegationTokenService creates a new DelegationTokenService
func NewDelegationTokenService(repo interfaces.DelegationTokenRepository) interfaces.DelegationTokenService {
	return &delegationTokenService{repo: repo}
}

// CreateToken creates a new delegation token and returns the plain token (only time it's returned).
func (s *delegationTokenService) CreateToken(ctx context.Context, req interfaces.DelegationTokenCreateRequest) (*interfaces.DelegationTokenCreateResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(req.Scopes) == 0 {
		return nil, errors.New("at least one scope is required")
	}

	expiresAt, err := parseExpiresIn(req.ExpiresIn)
	if err != nil {
		return nil, err
	}

	plain, err := generateDelegationToken()
	if err != nil {
		return nil, err
	}

	token := &types.DelegationToken{
		TokenHash:  types.HashDelegationToken(plain),
		TenantID:   req.TenantID,
		Name:       name,
		Scopes:     req.Scopes,
		AllowedIPs: req.AllowedIPs,
		ExpiresAt:  expiresAt,
		CreatedBy:  req.CreatedBy,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.repo.Create(ctx, token); err != nil {
		return nil, err
	}

	return &interfaces.DelegationTokenCreateResult{
		Token: token,
		Plain: plain,
	}, nil
}

// ValidateToken validates a plain token and returns the resolved token record.
func (s *delegationTokenService) ValidateToken(ctx context.Context, plainToken string) (*types.DelegationToken, error) {
	hash := types.HashDelegationToken(plainToken)
	token, err := s.repo.GetByTokenHash(ctx, hash)
	if err != nil || token == nil {
		return nil, errors.New("invalid delegation token")
	}
	if !token.IsValid() {
		return nil, errors.New("delegation token expired or revoked")
	}
	return token, nil
}

// ListTokens returns all tokens for a tenant.
func (s *delegationTokenService) ListTokens(ctx context.Context, tenantID uint64) ([]*types.DelegationToken, error) {
	return s.repo.ListByTenantID(ctx, tenantID)
}

// RevokeToken revokes a token by ID.
func (s *delegationTokenService) RevokeToken(ctx context.Context, id uint64) error {
	return s.repo.Revoke(ctx, id)
}

// DeleteToken permanently deletes a token.
func (s *delegationTokenService) DeleteToken(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// parseExpiresIn parses a duration string like "1h", "7d", "30d" into time.Time.
func parseExpiresIn(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().Add(30 * 24 * time.Hour), nil // default 30 days
	}

	var d time.Duration
	switch {
	case strings.HasSuffix(s, "h"):
		var hours int
		if _, err := parsePositiveInt(s[:len(s)-1], &hours); err != nil {
			return time.Time{}, errors.New("invalid expires_in format")
		}
		d = time.Duration(hours) * time.Hour
	case strings.HasSuffix(s, "d"):
		var days int
		if _, err := parsePositiveInt(s[:len(s)-1], &days); err != nil {
			return time.Time{}, errors.New("invalid expires_in format")
		}
		d = time.Duration(days) * 24 * time.Hour
	default:
		return time.Time{}, errors.New("expires_in must end in h (hours) or d (days)")
	}
	return time.Now().Add(d), nil
}

func parsePositiveInt(s string, out *int) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid number")
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

// generateDelegationToken generates a cryptographically random token.
func generateDelegationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
