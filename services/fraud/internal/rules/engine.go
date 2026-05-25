// internal/rules/engine.go
//
// The fraud rule engine evaluates a payment request against
// a set of rules and produces a composite risk score.

package rules

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
)

// Rule is the interface every fraud rule implements.
type Rule interface {
	Name() string
	Evaluate(ctx context.Context, req domain.CheckRequest) domain.RuleResult
}

// Engine runs all rules against a payment request.
type Engine struct {
	rules  []Rule
	logger *zap.Logger
}

func NewEngine(rules []Rule, logger *zap.Logger) *Engine {
	return &Engine{rules: rules, logger: logger}
}

// Evaluate runs all rules and returns a composite risk score.
// Rules are evaluated in order — cheapest first (no I/O),
// then Redis lookups, then database lookups.
// Stops early if score exceeds the block threshold.
func (e *Engine) Evaluate(ctx context.Context, req domain.CheckRequest) domain.CheckResponse {
	start := time.Now()

	var totalScore int
	var reasons []string

	for _, rule := range e.rules {
		result := rule.Evaluate(ctx, req)

		if result.Triggered {
			totalScore += result.Score
			reasons = append(reasons, result.Reason)

			e.logger.Debug("fraud rule triggered",
				zap.String("rule", result.RuleName),
				zap.Int("score", result.Score),
				zap.String("reason", result.Reason),
			)

			// Early exit — already over block threshold
			if totalScore >= 71 {
				break
			}
		}
	}

	// Cap at 100
	if totalScore > 100 {
		totalScore = 100
	}

	latency := time.Since(start).Milliseconds()

	response := domain.CheckResponse{
		TransactionID: req.TransactionID,
		Decision:      domain.ScoreToDecision(totalScore),
		RiskScore:     totalScore,
		RiskLevel:     domain.ScoreToRiskLevel(totalScore),
		Reasons:       reasons,
		ProcessedAt:   time.Now().UTC(),
		LatencyMs:     latency,
	}

	e.logger.Info("fraud check completed",
		zap.String("transaction_id", req.TransactionID.String()),
		zap.String("decision", string(response.Decision)),
		zap.Int("risk_score", totalScore),
		zap.Int64("latency_ms", latency),
	)

	return response
}
