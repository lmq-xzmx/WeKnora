package types

import (
	"crypto/sha256"
	"time"
)

// DelegationToken is a short-lived, scope-restricted token issued by a tenant
// to authorize a third-party system to access specific resources.
type DelegationToken struct {
	ID          uint64      `json:"id" gorm:"primaryKey"`
	TokenHash   string      `json:"-" gorm:"column:token_hash;uniqueIndex;not null"`
	TenantID    uint64      `json:"tenant_id" gorm:"not null;index"`
	Name        string      `json:"name" gorm:"type:varchar(128);not null"`
	Scopes      StringArray `json:"scopes" gorm:"type:text;not null;default:'[]'"`
	AllowedIPs  StringArray `json:"allowed_ips" gorm:"type:text;not null;default:'[]'"`
	ExpiresAt   time.Time   `json:"expires_at" gorm:"not null;index"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" gorm:"index"`
	CreatedBy   string      `json:"created_by" gorm:"type:varchar(128);not null"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// TableName returns the DB table name.
func (DelegationToken) TableName() string {
	return "delegation_tokens"
}

// IsExpired returns true if the token has expired.
func (t *DelegationToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// IsRevoked returns true if the token has been manually revoked.
func (t *DelegationToken) IsRevoked() bool {
	return t.RevokedAt != nil
}

// IsValid returns true if the token is neither expired nor revoked.
func (t *DelegationToken) IsValid() bool {
	return !t.IsExpired() && !t.IsRevoked()
}

// DelegationTokenScope carries the resolved identity of a delegation token
// bearer for authorization decisions.
type DelegationTokenScope struct {
	TokenID  uint64
	Name     string
	TenantID uint64
	Scopes   []string
}

// HashDelegationToken returns the SHA-256 hash of a plain token.
func HashDelegationToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return string(h[:])
}
