// internal/handler/admin_handler.go
//
// Admin-only endpoints for internal operations.
// Protected by a static API key — never exposed to the public internet.
// In production this port is firewalled — accessible only via VPN or
// internal service mesh.
//
// These endpoints exist for:
// 1. Testing the full payment flow in development
// 2. Manual funding operations by the ops team
// 3. Will be replaced by Paystack/NIBSS webhooks in Phase 7

package handler

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/payment/internal/domain"
	"github.com/Ad3bay0c/payflow/payment/internal/service"
)

type AdminHandler struct {
	paymentSvc service.PaymentService
	logger     *zap.Logger
}

func NewAdminHandler(paymentSvc service.PaymentService, logger *zap.Logger) *AdminHandler {
	return &AdminHandler{
		paymentSvc: paymentSvc,
		logger:     logger,
	}
}

func (h *AdminHandler) RegisterRoutes(rg *gin.RouterGroup) {
	// All admin routes require the admin API key
	rg.Use(requireAdminKey())
	rg.POST("/wallets/:id/fund", h.FundWallet)
}

// RegisterInternalRoutes adds internal-only routes for service-to-service calls.
func (h *AdminHandler) RegisterInternalRoutes(rg *gin.RouterGroup) {
	rg.Use(requireAdminKey())
	rg.GET("/wallets/:id/owner", h.GetWalletOwner)
}

func (h *AdminHandler) GetWalletOwner(c *gin.Context) {
	walletID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "invalid wallet ID")
		return
	}

	wallet, err := h.paymentSvc.GetWallet(c.Request.Context(), walletID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "wallet not found")
		return
	}

	// We need the user's phone from the auth service
	// For now return the user_id — notification service
	// can call auth service directly for the phone
	ok(c, gin.H{
		"user_id":   wallet.UserID.String(),
		"wallet_id": wallet.ID.String(),
	})
}

// FundWallet credits a wallet directly.
// Only callable with a valid admin API key.
// In production — replaced by Paystack/NIBSS webhook handlers in Phase 7.
func (h *AdminHandler) FundWallet(c *gin.Context) {
	walletID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "invalid wallet ID")
		return
	}

	var req struct {
		IdempotencyKey string `json:"idempotency_key" binding:"required"`
		Amount         int64  `json:"amount_kobo"     binding:"required,min=1"`
		Description    string `json:"description"`
		Source         string `json:"source"` // "bank_transfer" | "card" | "admin"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	txn, err := h.paymentSvc.FundWallet(c.Request.Context(), domain.FundWalletRequest{
		IdempotencyKey: req.IdempotencyKey,
		WalletID:       walletID,
		Amount:         req.Amount,
		Description:    req.Description,
	})
	if err != nil {
		h.logger.Error("admin fund wallet failed",
			zap.String("wallet_id", walletID.String()),
			zap.Error(err),
		)
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	h.logger.Info("wallet funded via admin",
		zap.String("wallet_id", walletID.String()),
		zap.Int64("amount_kobo", req.Amount),
		zap.String("source", req.Source),
	)

	ok(c, toTransactionResponse(txn))
}

// requireAdminKey validates the admin API key on every request.
// The key is loaded from environment — never hardcoded.
// In production, rotated quarterly and stored in AWS Secrets Manager.
func requireAdminKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		adminKey := os.Getenv("ADMIN_API_KEY")
		if adminKey == "" {
			// No admin key configured — block all admin requests
			fail(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "admin access not configured")
			c.Abort()
			return
		}

		providedKey := c.GetHeader("X-Admin-Key")
		if providedKey == "" {
			// Also check Authorization header with "AdminKey" scheme
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "AdminKey ") {
				providedKey = strings.TrimPrefix(authHeader, "AdminKey ")
			}
		}

		if providedKey == "" {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "admin API key required")
			c.Abort()
			return
		}

		// Constant time comparison — prevents timing attacks
		if !secureCompare(providedKey, adminKey) {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "invalid admin API key")
			c.Abort()
			return
		}

		c.Next()
	}
}

// secureCompare compares two strings in constant time.
// Prevents timing attacks where an attacker measures response time
// to guess the correct API key character by character.
func secureCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}
