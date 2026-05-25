// internal/handler/fraud_handler.go

package handler

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
	"github.com/Ad3bay0c/payflow/fraud/internal/service"
)

type FraudHandler struct {
	fraudSvc service.FraudService
	logger   *zap.Logger
}

func NewFraudHandler(fraudSvc service.FraudService, logger *zap.Logger) *FraudHandler {
	return &FraudHandler{
		fraudSvc: fraudSvc,
		logger:   logger,
	}
}

func (h *FraudHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.Use(requireServiceKey())
	rg.POST("/check", h.Check)
	rg.POST("/record", h.Record)
}

// Check evaluates a payment request for fraud risk.
// Called synchronously by the payment service before processing.
// Must respond within 80ms.
func (h *FraudHandler) Check(c *gin.Context) {
	var req domain.CheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	// Set requested_at if not provided
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}

	response := h.fraudSvc.Check(c.Request.Context(), req)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// Record updates velocity counters after an approved transfer completes.
// Called by the payment service after successful commit.
func (h *FraudHandler) Record(c *gin.Context) {
	var req domain.CheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	h.fraudSvc.RecordApprovedTransfer(c.Request.Context(), req)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"recorded": true},
	})
}

// requireServiceKey protects internal endpoints.
// In production this is replaced by mTLS — services authenticate
// via certificates, not API keys.
func requireServiceKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		serviceKey := os.Getenv("SERVICE_API_KEY")
		if serviceKey == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not configured"})
			c.Abort()
			return
		}

		provided := c.GetHeader("X-Service-Key")
		if provided == "" {
			auth := c.GetHeader("Authorization")
			if strings.HasPrefix(auth, "ServiceKey ") {
				provided = strings.TrimPrefix(auth, "ServiceKey ")
			}
		}

		if !secureCompare(provided, serviceKey) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid service key"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func secureCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
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
