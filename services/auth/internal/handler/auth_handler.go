// internal/handler/auth_handler.go
//
// HTTP handlers for the auth service.
// Handlers are intentionally thin:
//   1. Parse + validate the request
//   2. Call the service
//   3. Return a response
//
// No business logic lives here.

package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/auth/internal/domain"
	"github.com/Ad3bay0c/payflow/auth/internal/service"
)

type AuthHandler struct {
	authSvc service.AuthService
	jwtSvc  service.JWTService
	logger  *zap.Logger
}

func NewAuthHandler(
	authSvc service.AuthService,
	jwtSvc service.JWTService,
	logger *zap.Logger,
) *AuthHandler {
	return &AuthHandler{
		authSvc: authSvc,
		jwtSvc:  jwtSvc,
		logger:  logger,
	}
}

// RegisterRoutes attaches all auth routes to a Gin router group.
func (h *AuthHandler) RegisterRoutes(rg *gin.RouterGroup) {
	// Public routes — no auth required
	rg.POST("/request-otp", h.RequestOTP)
	rg.POST("/verify-otp", h.VerifyOTP)
	rg.POST("/register", h.Register)
	rg.POST("/login", h.Login)
	rg.POST("/refresh", h.RefreshToken)

	rg.POST("/validate-token", h.ValidateToken)

	// Protected routes — valid JWT required
	protected := rg.Group("")
	protected.Use(h.RequireAuth())
	protected.POST("/logout", h.Logout)
	protected.GET("/me", h.Me)
}

type requestOTPRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
}

type verifyOTPRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
	Code        string `json:"code"         binding:"required,len=6"`
}

type registerRequest struct {
	PhoneNumber string  `json:"phone_number" binding:"required"`
	FullName    string  `json:"full_name"    binding:"required"`
	Email       *string `json:"email"`
	Password    string  `json:"password"     binding:"required,min=8"`
}

type loginRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
	Password    string `json:"password"     binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RequestOTP godoc
// POST /api/v1/auth/request-otp
// Sends a 6-digit OTP to the provided phone number.
// Rate limited to 3 requests per 10 minutes.
func (h *AuthHandler) RequestOTP(c *gin.Context) {
	var req requestOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	if err := h.authSvc.RequestOTP(c.Request.Context(), req.PhoneNumber); err != nil {
		// Check if it's a rate limit error
		if strings.Contains(err.Error(), "rate limit exceeded") {
			fail(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", err.Error())
			return
		}
		h.logger.Error("request otp failed", zap.Error(err))
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to send OTP")
		return
	}

	ok(c, gin.H{"message": "OTP sent successfully"})
}

// VerifyOTP godoc
// POST /api/v1/auth/verify-otp
// Validates the OTP sent to the user's phone.
func (h *AuthHandler) VerifyOTP(c *gin.Context) {
	var req verifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	verified, err := h.authSvc.VerifyOTP(c.Request.Context(), req.PhoneNumber, req.Code)
	if err != nil {
		fail(c, http.StatusBadRequest, "OTP_ERROR", err.Error())
		return
	}
	if !verified {
		fail(c, http.StatusBadRequest, "INVALID_OTP", "invalid OTP code")
		return
	}

	ok(c, gin.H{
		"verified": true,
		"message":  "phone number verified successfully",
	})
}

// Register godoc
// POST /api/v1/auth/register
// Creates a new PayFlow account. Requires prior OTP verification.
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	user, err := h.authSvc.Register(c.Request.Context(), service.RegisterRequest{
		PhoneNumber: req.PhoneNumber,
		FullName:    req.FullName,
		Email:       req.Email,
		Password:    req.Password,
	})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "already exists"):
			fail(c, http.StatusConflict, "CONFLICT", err.Error())
		case strings.Contains(err.Error(), "must be verified"):
			fail(c, http.StatusBadRequest, "PHONE_NOT_VERIFIED", err.Error())
		default:
			h.logger.Error("register failed", zap.Error(err))
			fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "registration failed")
		}
		return
	}

	created(c, user.ToUserResponse())
}

// Login godoc
// POST /api/v1/auth/login
// Authenticates a user and returns a JWT token pair.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	tokens, err := h.authSvc.Login(c.Request.Context(), service.LoginRequest{
		PhoneNumber: req.PhoneNumber,
		Password:    req.Password,
	})
	if err != nil {
		// Always 401 for auth failures — never reveal which field was wrong
		fail(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials")
		return
	}

	// Refresh token goes in HttpOnly cookie — not accessible to JavaScript
	// This protects against XSS attacks stealing the refresh token
	c.SetCookie(
		"refresh_token",
		tokens.RefreshToken,
		int(domain.RefreshTokenTTL.Seconds()),
		"/api/v1/auth/refresh",
		"",   // domain — empty means same domain
		true, // secure — HTTPS only (set to false locally for testing)
		true, // httpOnly — not accessible to JavaScript
	)

	ok(c, gin.H{
		"access_token": tokens.AccessToken,
		"expires_at":   tokens.ExpiresAt,
		"token_type":   tokens.TokenType,
	})
}

// RefreshToken godoc
// POST /api/v1/auth/refresh
// Issues a new token pair using a valid refresh token.
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	// Try cookie first, then request body
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil {
		var req refreshRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "refresh token required")
			return
		}
		refreshToken = req.RefreshToken
	}

	tokens, err := h.authSvc.RefreshToken(c.Request.Context(), refreshToken)
	if err != nil {
		fail(c, http.StatusUnauthorized, "INVALID_TOKEN", "invalid or expired refresh token")
		return
	}

	// Rotate the cookie
	c.SetCookie(
		"refresh_token",
		tokens.RefreshToken,
		int(domain.RefreshTokenTTL.Seconds()),
		"/api/v1/auth/refresh",
		"",
		true,
		true,
	)

	ok(c, gin.H{
		"access_token": tokens.AccessToken,
		"expires_at":   tokens.ExpiresAt,
		"token_type":   tokens.TokenType,
	})
}

// Logout godoc
// POST /api/v1/auth/logout
// Revokes the current access token. Requires valid JWT.
func (h *AuthHandler) Logout(c *gin.Context) {
	accessTokenI, exists := c.Get("access_token")
	if !exists {
		fail(c, http.StatusUnauthorized, "UNAUTHORISED", "not authenticated")
		return
	}
	accessToken, exists := accessTokenI.(string)
	if !exists {
		fail(c, http.StatusUnauthorized, "UNAUTHORISED", "not authenticated")
		return
	}

	if err := h.authSvc.Logout(c.Request.Context(), accessToken); err != nil {
		h.logger.Error("logout failed", zap.Error(err))
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "logout failed")
		return
	}

	// Clear the refresh token cookie
	c.SetCookie("refresh_token", "", -1, "/api/v1/auth/refresh", "", true, true)

	ok(c, gin.H{"message": "logged out successfully"})
}

// Me godoc
// GET /api/v1/auth/me
// Returns the current authenticated user's profile.
func (h *AuthHandler) Me(c *gin.Context) {
	userIDStr, exists := c.Get("user_id")
	if !exists {
		fail(c, http.StatusUnauthorized, "UNAUTHORISED", "not authenticated")
		return
	}

	userID, err := uuid.Parse(userIDStr.(string))
	if err != nil {
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "invalid user context")
		return
	}

	user, err := h.authSvc.GetUser(c.Request.Context(), userID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "user not found")
		return
	}

	ok(c, user.ToUserResponse())
}

// ValidateToken is an internal endpoint called by other services
// to verify a token is valid AND not revoked.
// Protected by mTLS in production — not exposed to the public internet.
func (h *AuthHandler) ValidateToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	claims, err := h.jwtSvc.ValidateToken(c.Request.Context(), req.Token, "access")
	if err != nil {
		fail(c, http.StatusUnauthorized, "INVALID_TOKEN", "token is invalid or revoked")
		return
	}

	// Return the claims so the calling service doesn't need to parse the token again
	ok(c, gin.H{
		"valid":      true,
		"user_id":    claims.UserID.String(),
		"tier":       claims.Tier,
		"kyc_status": claims.KYCStatus,
	})
}
