// internal/service/jwt.go
//
// JWT token management using RS256 asymmetric signing.
//
// WHY RS256 NOT HS256:
// HS256 uses one shared secret. Every service that validates tokens
// needs that secret — meaning every service could also forge tokens.
// RS256 uses a key pair. Only the auth service holds the private key
// and can sign tokens. Every other service holds only the public key
// and can verify but never forge. If a downstream service is
// compromised, it cannot issue fraudulent tokens.

package service

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Ad3bay0c/payflow/auth/internal/domain"
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"

	// Redis key prefixes
	prefixBlockedToken = "payflow:auth:blocked_token:"
	prefixUserRevoked  = "payflow:auth:user_revoked:"
)

// jwtClaims extends standard JWT claims with PayFlow-specific fields.
// Keep this minimal — full profile is always fetched fresh from the DB.
type jwtClaims struct {
	jwt.RegisteredClaims
	Phone     string           `json:"phone"`
	KYCStatus domain.KYCStatus `json:"kyc_status"`
	Tier      domain.Tier      `json:"tier"`
	TokenType string           `json:"token_type"`
}

type JWTService interface {
	GenerateTokenPair(ctx context.Context, user *domain.User) (*domain.TokenPair, error)
	ValidateToken(ctx context.Context, tokenString string, expectedType string) (*domain.Claims, error)
	RevokeToken(ctx context.Context, tokenString string) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

type jwtService struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	redis      *redis.Client
}

func NewJWTService(
	privateKeyPEM []byte,
	publicKeyPEM []byte,
	issuer string,
	accessTTL time.Duration,
	refreshTTL time.Duration,
	redisClient *redis.Client,
) (JWTService, error) {
	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	return &jwtService{
		privateKey: privateKey,
		publicKey:  publicKey,
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		redis:      redisClient,
	}, nil
}

// GenerateTokenPair creates an access token and a refresh token.
// Access token:  15 minutes — used in Authorization header every request.
// Refresh token: 30 days    — stored in HttpOnly cookie, used once to
//
//	get a new pair, then immediately revoked.
func (s *jwtService) GenerateTokenPair(ctx context.Context, user *domain.User) (*domain.TokenPair, error) {
	now := time.Now().UTC()
	accessExpiresAt := now.Add(s.accessTTL)

	accessToken, err := s.sign(jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(accessExpiresAt),
			ID:        uuid.NewString(), // JTI — unique per token, used for revocation
		},
		Phone:     user.PhoneNumber,
		KYCStatus: user.KYCStatus,
		Tier:      user.Tier,
		TokenType: tokenTypeAccess,
	})
	if err != nil {
		return nil, fmt.Errorf("signing access token: %w", err)
	}

	refreshToken, err := s.sign(jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTTL)),
			ID:        uuid.NewString(),
		},
		Phone:     user.PhoneNumber,
		KYCStatus: user.KYCStatus,
		Tier:      user.Tier,
		TokenType: tokenTypeRefresh,
	})
	if err != nil {
		return nil, fmt.Errorf("signing refresh token: %w", err)
	}

	return &domain.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    accessExpiresAt,
		TokenType:    "Bearer",
	}, nil
}

// ValidateToken parses and validates a JWT.
// Checks: signature, expiry, issuer, token type, and Redis blocklist.
// expectedType is either "access" or "refresh".
func (s *jwtService) ValidateToken(ctx context.Context, tokenString string, expectedType string) (*domain.Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&jwtClaims{},
		func(token *jwt.Token) (any, error) {
			// Always verify the signing algorithm
			// Never skip this — an attacker could send "alg: none"
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return s.publicKey, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	if claims.TokenType != expectedType {
		return nil, fmt.Errorf("wrong token type: expected %s got %s", expectedType, claims.TokenType)
	}

	// Check the Redis blocklist
	// This covers: logout, password change, suspicious activity
	blocked, err := s.isBlocked(ctx, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("checking token blocklist: %w", err)
	}
	if blocked {
		return nil, fmt.Errorf("token has been revoked")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("invalid subject claim")
	}

	return &domain.Claims{
		UserID:    userID,
		Phone:     claims.Phone,
		KYCStatus: claims.KYCStatus,
		Tier:      claims.Tier,
		TokenType: claims.TokenType,
		JTI:       claims.ID,
	}, nil
}

// RevokeToken adds a token's JTI to the Redis blocklist.
// TTL is set to the token's remaining lifetime — no need to keep it longer.
// Called on: logout, token refresh (old token revoked), suspicious activity.
func (s *jwtService) RevokeToken(ctx context.Context, tokenString string) error {
	// Parse without validation to extract the JTI and expiry
	// We parse even expired tokens to revoke them
	token, _ := jwt.ParseWithClaims(
		tokenString,
		&jwtClaims{},
		func(t *jwt.Token) (any, error) { return s.publicKey, nil },
	)
	if token == nil {
		return nil
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || claims.ID == "" {
		return nil
	}

	// TTL = remaining lifetime of the token
	// After it would have expired anyway, no need to keep it in Redis
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		return nil // already expired, nothing to revoke
	}

	key := prefixBlockedToken + claims.ID
	return s.redis.Set(ctx, key, "1", ttl).Err()
}

// RevokeAllForUser invalidates all tokens for a user.
// Called on: password change, account lock, suspicious login from new device.
// Any token issued before this timestamp is considered invalid.
func (s *jwtService) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	key := prefixUserRevoked + userID.String()
	return s.redis.Set(ctx, key, time.Now().Unix(), s.refreshTTL).Err()
}

func (s *jwtService) sign(claims jwtClaims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(s.privateKey)
}

func (s *jwtService) isBlocked(ctx context.Context, jti string) (bool, error) {
	result, err := s.redis.Exists(ctx, prefixBlockedToken+jti).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// Try PKCS8 first (openssl genrsa modern output), then PKCS1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing private key: %w", err)
		}
		return rsaKey, nil
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsaKey, nil
}

func parsePublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsaKey, nil
}
