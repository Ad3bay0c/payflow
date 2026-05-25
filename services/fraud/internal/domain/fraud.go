// internal/domain/fraud.go

package domain

import (
	"time"

	"github.com/google/uuid"
)

// Decision is the fraud service's verdict on a payment request.
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionBlock Decision = "BLOCK"
	DecisionFlag  Decision = "FLAG" // allow but flag for manual review
)

// RiskLevel maps score ranges to human-readable levels.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"    // 0–30
	RiskMedium RiskLevel = "medium" // 31–70
	RiskHigh   RiskLevel = "high"   // 71–100
)

// CheckRequest is what the payment service sends to the fraud service.
type CheckRequest struct {
	// Payment details
	TransactionID    uuid.UUID `json:"transaction_id"`
	SenderWalletID   uuid.UUID `json:"sender_wallet_id"`
	ReceiverWalletID uuid.UUID `json:"receiver_wallet_id"`
	SenderUserID     uuid.UUID `json:"sender_user_id"`
	Amount           int64     `json:"amount_kobo"`
	Currency         string    `json:"currency"`

	// Sender context — helps rules make better decisions
	SenderTier      int16  `json:"sender_tier"`
	SenderKYCStatus string `json:"sender_kyc_status"`

	// Request metadata
	IPAddress   string    `json:"ip_address"`
	DeviceID    string    `json:"device_id"`
	RequestedAt time.Time `json:"requested_at"`
}

// CheckResponse is what the fraud service returns to the payment service.
type CheckResponse struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Decision      Decision  `json:"decision"`
	RiskScore     int       `json:"risk_score"`
	RiskLevel     RiskLevel `json:"risk_level"`
	Reasons       []string  `json:"reasons"` // why the risk score
	ProcessedAt   time.Time `json:"processed_at"`
	// How long the fraud check took — monitored to ensure < 80ms
	LatencyMs int64 `json:"latency_ms"`
}

// RuleResult is the output of a single fraud rule.
type RuleResult struct {
	RuleName  string
	Score     int    // contribution to total risk score
	Triggered bool   // did this rule fire?
	Reason    string // human-readable explanation
}

// ScoreToRiskLevel maps a numeric score to a risk level.
func ScoreToRiskLevel(score int) RiskLevel {
	switch {
	case score <= 30:
		return RiskLow
	case score <= 70:
		return RiskMedium
	default:
		return RiskHigh
	}
}

// ScoreToDecision maps a numeric score to a decision.
func ScoreToDecision(score int) Decision {
	switch {
	case score <= 30:
		return DecisionAllow
	case score <= 70:
		return DecisionFlag
	default:
		return DecisionBlock
	}
}
