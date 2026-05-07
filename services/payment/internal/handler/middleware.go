// internal/handler/middleware.go

package handler

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/Ad3bay0c/payflow/payment/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// jwtClaims mirrors the auth service claims structure.
// The payment service only needs to verify — it never issues tokens.
type jwtClaims struct {
	jwt.RegisteredClaims
	Phone     string `json:"phone"`
	KYCStatus string `json:"kyc_status"`
	Tier      int16  `json:"tier"`
	TokenType string `json:"token_type"`
}

// RequireAuth validates the JWT access token.
// The payment service uses the auth service's PUBLIC key — it can
// verify tokens but never issue them. This is the RS256 advantage.
func RequireAuth(publicKeyPEM []byte) gin.HandlerFunc {
	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		panic(fmt.Sprintf("invalid public key: %v", err))
	}

	return func(c *gin.Context) {
		token, rawToken, err := extractAndValidateToken(c, publicKey)
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", err.Error())
			c.Abort()
			return
		}

		claims := token.Claims.(*jwtClaims)
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "invalid token subject")
			c.Abort()
			return
		}

		c.Set("user_id", userID)
		c.Set("tier", claims.Tier)
		c.Set("kyc_status", claims.KYCStatus)
		c.Set("access_token", rawToken)
		c.Next()
	}
}

// RequireAuthStrict performs full token introspection via the auth service.
// Used for high-value transfers — confirms the token is not revoked.
// Falls back to local validation if the auth service is unreachable —
// accepting a small security risk over blocking all high-value transfers.
func RequireAuthStrict(publicKeyPEM []byte, validator auth.TokenValidator, logger *zap.Logger) gin.HandlerFunc {
	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		panic(fmt.Sprintf("invalid public key: %v", err))
	}

	return func(c *gin.Context) {
		// Local validation first (fast path)
		token, rawToken, err := extractAndValidateToken(c, publicKey)
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", err.Error())
			c.Abort()
			return
		}

		claims := token.Claims.(*jwtClaims)
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "invalid token subject")
			c.Abort()
			return
		}

		// Introspection against auth service
		// Confirms token is not in the revocation list
		result, err := validator.Validate(c.Request.Context(), rawToken)
		if err != nil {
			// Auth service unreachable — log and fall back to local validation
			// We accept this tradeoff: blocking all transfers when auth is down
			// is worse than the small window of a revoked token being used
			logger.Warn("auth service introspection failed — falling back to local validation",
				zap.Error(err),
				zap.String("user_id", userID.String()),
			)
		} else if !result.Valid {
			// Auth service confirmed token is revoked
			fail(c, http.StatusUnauthorized, "TOKEN_REVOKED", "token has been revoked")
			c.Abort()
			return
		}

		c.Set("user_id", userID)
		c.Set("tier", claims.Tier)
		c.Set("kyc_status", claims.KYCStatus)
		c.Set("access_token", rawToken)
		c.Next()
	}
}

// TraceID injects a unique trace_id into every request.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		c.Set("trace_id", traceID)
		c.Header("X-Trace-ID", traceID)
		c.Next()
	}
}

func extractAndValidateToken(c *gin.Context, publicKey *rsa.PublicKey) (*jwt.Token, string, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return nil, "", fmt.Errorf("missing authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return nil, "", fmt.Errorf("invalid authorization header format")
	}

	rawToken := parts[1]

	token, err := jwt.ParseWithClaims(rawToken, &jwtClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return publicKey, nil
		},
	)
	if err != nil || !token.Valid {
		return nil, "", fmt.Errorf("invalid or expired token")
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || claims.TokenType != "access" {
		return nil, "", fmt.Errorf("invalid token type")
	}

	return token, rawToken, nil
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
		return nil, fmt.Errorf("not an RSA public key")
	}
	return rsaKey, nil
}
