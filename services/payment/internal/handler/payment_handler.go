// internal/handler/payment_handler.go

package handler

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/payment/internal/auth"
	"github.com/Ad3bay0c/payflow/payment/internal/domain"
	"github.com/Ad3bay0c/payflow/payment/internal/service"
)

type PaymentHandler struct {
	paymentSvc service.PaymentService
	validator  auth.TokenValidator
	logger     *zap.Logger
}

func NewPaymentHandler(
	paymentSvc service.PaymentService,
	validator auth.TokenValidator,
	logger *zap.Logger,
) *PaymentHandler {
	return &PaymentHandler{
		paymentSvc: paymentSvc,
		validator:  validator,
		logger:     logger,
	}
}

func (h *PaymentHandler) RegisterRoutes(
	rg *gin.RouterGroup,
	publicKeyPEM []byte,
) {
	// Standard auth — local JWT validation only
	// Used for read operations and low-value writes
	standard := rg.Group("")
	standard.Use(RequireAuth(publicKeyPEM))
	standard.POST("/wallets", h.CreateWallet)
	standard.GET("/wallets/me", h.GetMyWallet)
	standard.GET("/wallets/:id", h.GetWallet)
	standard.GET("/payments/:id", h.GetTransaction)
	standard.GET("/payments", h.ListTransactions)

	// Strict auth — local + auth service introspection
	// Used for transfers — money leaves a wallet
	strict := rg.Group("")
	strict.Use(RequireAuthStrict(publicKeyPEM, h.validator, h.logger))
	strict.POST("/payments/transfer", h.Transfer)
}

type fundWalletRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	Amount         int64  `json:"amount_kobo"     binding:"required,min=1"`
	Description    string `json:"description"`
}

type transferRequest struct {
	IdempotencyKey   string `json:"idempotency_key"    binding:"required"`
	ReceiverWalletID string `json:"receiver_wallet_id" binding:"required,uuid"`
	Amount           int64  `json:"amount_kobo"        binding:"required,min=1"`
	Description      string `json:"description"`
}

func (h *PaymentHandler) CreateWallet(c *gin.Context) {
	userID := getUserID(c)

	wallet, err := h.paymentSvc.CreateWallet(c.Request.Context(), userID)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fail(c, http.StatusConflict, "CONFLICT", err.Error())
			return
		}
		h.logger.Error("create wallet failed", zap.Error(err))
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create wallet")
		return
	}

	created(c, toWalletResponse(wallet))
}

func (h *PaymentHandler) GetMyWallet(c *gin.Context) {
	userID := getUserID(c)

	wallet, err := h.paymentSvc.GetWalletByUserID(c.Request.Context(), userID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "wallet not found")
		return
	}

	ok(c, toWalletResponse(wallet))
}

func (h *PaymentHandler) GetWallet(c *gin.Context) {
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

	ok(c, toWalletResponse(wallet))
}

func (h *PaymentHandler) Transfer(c *gin.Context) {
	var req transferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	receiverID, err := uuid.Parse(req.ReceiverWalletID)
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "invalid receiver wallet ID")
		return
	}

	// Get sender wallet from their user ID
	userID := getUserID(c)
	senderWallet, err := h.paymentSvc.GetWalletByUserID(c.Request.Context(), userID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "sender wallet not found")
		return
	}

	tier := getTier(c)

	txn, err := h.paymentSvc.Transfer(c.Request.Context(), domain.TransferRequest{
		IdempotencyKey:   req.IdempotencyKey,
		SenderWalletID:   senderWallet.ID,
		ReceiverWalletID: receiverID,
		Amount:           req.Amount,
		Description:      req.Description,
		SenderTier:       tier,
	})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "insufficient balance"):
			fail(c, http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", err.Error())
		case strings.Contains(err.Error(), "exceeds your tier"):
			fail(c, http.StatusForbidden, "TIER_LIMIT_EXCEEDED", err.Error())
		case strings.Contains(err.Error(), "daily limit"):
			fail(c, http.StatusForbidden, "DAILY_LIMIT_EXCEEDED", err.Error())
		case strings.Contains(err.Error(), "same wallet"):
			fail(c, http.StatusBadRequest, "INVALID_TRANSFER", err.Error())
		default:
			h.logger.Error("transfer failed", zap.Error(err))
			fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "transfer failed")
		}
		return
	}

	ok(c, toTransactionResponse(txn))
}

func (h *PaymentHandler) GetTransaction(c *gin.Context) {
	txnID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "invalid transaction ID")
		return
	}

	txn, err := h.paymentSvc.GetTransaction(c.Request.Context(), txnID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "transaction not found")
		return
	}

	ok(c, toTransactionResponse(txn))
}

func (h *PaymentHandler) ListTransactions(c *gin.Context) {
	userID := getUserID(c)

	// Pagination params — default page 1, 20 per page
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))

	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	offset := int32((page - 1) * perPage)

	// Get the user's wallet first
	wallet, err := h.paymentSvc.GetWalletByUserID(c.Request.Context(), userID)
	if err != nil {
		fail(c, http.StatusNotFound, "NOT_FOUND", "wallet not found")
		return
	}

	txns, total, err := h.paymentSvc.ListTransactions(
		c.Request.Context(),
		wallet.ID,
		int32(perPage),
		offset,
	)
	if err != nil {
		h.logger.Error("list transactions failed", zap.Error(err))
		fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list transactions")
		return
	}

	totalPages := int(math.Ceil(float64(total) / float64(perPage)))

	responses := make([]transactionResponse, len(txns))
	for i, txn := range txns {
		responses[i] = toTransactionResponse(txn)
	}

	paginated(c, responses, &meta{
		Page:       page,
		PerPage:    perPage,
		TotalCount: total,
		TotalPages: totalPages,
	})
}

type walletResponse struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	BalanceKobo  int64   `json:"balance_kobo"`
	BalanceNaira float64 `json:"balance_naira"`
	Currency     string  `json:"currency"`
	IsActive     bool    `json:"is_active"`
	CreatedAt    string  `json:"created_at"`
}

type transactionResponse struct {
	ID               string  `json:"id"`
	IdempotencyKey   string  `json:"idempotency_key"`
	SenderWalletID   *string `json:"sender_wallet_id,omitempty"`
	ReceiverWalletID *string `json:"receiver_wallet_id,omitempty"`
	AmountKobo       int64   `json:"amount_kobo"`
	AmountNaira      float64 `json:"amount_naira"`
	FeeKobo          int64   `json:"fee_kobo"`
	FeeNaira         float64 `json:"fee_naira"`
	Currency         string  `json:"currency"`
	Status           string  `json:"status"`
	Type             string  `json:"type"`
	Description      *string `json:"description,omitempty"`
	CompletedAt      *string `json:"completed_at,omitempty"`
	FailedAt         *string `json:"failed_at,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

func toWalletResponse(w *domain.Wallet) walletResponse {
	return walletResponse{
		ID:           w.ID.String(),
		UserID:       w.UserID.String(),
		BalanceKobo:  w.Balance,
		BalanceNaira: w.BalanceInNaira(),
		Currency:     w.Currency,
		IsActive:     w.IsActive,
		CreatedAt:    w.CreatedAt.Format(time.RFC3339),
	}
}

func toTransactionResponse(t *domain.Transaction) transactionResponse {
	resp := transactionResponse{
		ID:             t.ID.String(),
		IdempotencyKey: t.IdempotencyKey,
		AmountKobo:     t.Amount,
		AmountNaira:    t.AmountInNaira(),
		FeeKobo:        t.Fee,
		FeeNaira:       float64(t.Fee) / 100,
		Currency:       t.Currency,
		Status:         string(t.Status),
		Type:           string(t.Type),
		Description:    t.Description,
		CreatedAt:      t.CreatedAt.Format(time.RFC3339),
	}

	if t.SenderWalletID != nil {
		s := t.SenderWalletID.String()
		resp.SenderWalletID = &s
	}
	if t.ReceiverWalletID != nil {
		r := t.ReceiverWalletID.String()
		resp.ReceiverWalletID = &r
	}
	if t.CompletedAt != nil {
		s := t.CompletedAt.Format(time.RFC3339)
		resp.CompletedAt = &s
	}
	if t.FailedAt != nil {
		s := t.FailedAt.Format(time.RFC3339)
		resp.FailedAt = &s
	}

	return resp
}

func getUserID(c *gin.Context) uuid.UUID {
	if id, exists := c.Get("user_id"); exists {
		if uid, ok := id.(uuid.UUID); ok {
			return uid
		}
	}
	return uuid.Nil
}

func getTier(c *gin.Context) int16 {
	if tier, exists := c.Get("tier"); exists {
		if t, ok := tier.(int16); ok {
			return t
		}
	}
	return 1 // default to most restrictive tier
}
