// internal/domain/user.go

package domain

import (
	"time"

	"github.com/google/uuid"
)

type KYCStatus string

const (
	KYCPending  KYCStatus = "pending"  // just signed up
	KYCBasic    KYCStatus = "basic"    // phone number verified
	KYCVerified KYCStatus = "verified" // BVN verified — unlocks Tier 2 limits
	KYCFull     KYCStatus = "full"     // full document KYC — unlocks Tier 3 limits
)

type Tier int

const (
	Tier1 Tier = 1
	Tier2 Tier = 2
	Tier3 Tier = 3
)

type User struct {
	ID          uuid.UUID `json:"id"`
	PhoneNumber string    `json:"phone_number"`
	Email       *string   `json:"email,omitempty"`
	FullName    string    `json:"full_name"`
	KYCStatus   KYCStatus `json:"kyc_status"`
	Tier        Tier      `json:"tier"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	PasswordHash []byte     `json:"-"`
	DeletedAt    *time.Time `json:"-"`
}

// TokenPair is returned on successful login or token refresh.
// Access token → short lived (15 min), sent in Authorization header.
// Refresh token → long lived (30 days), stored in HttpOnly cookie.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"` // always "Bearer"
}

type Claims struct {
	UserID    uuid.UUID `json:"sub"`
	Phone     string    `json:"phone"`
	KYCStatus KYCStatus `json:"kyc_status"`
	Tier      Tier      `json:"tier"`
	TokenType string    `json:"token_type"` // "access" or "refresh"
	JTI       string    `json:"jti"`        // JWT ID — used for revocation
}
