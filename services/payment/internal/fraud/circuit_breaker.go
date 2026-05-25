// internal/fraud/circuit_breaker.go
//
// Circuit breaker for the fraud service client.
// If the fraud service fails or times out repeatedly, the circuit
// opens and we fall back to simple rule-based checks.
// This prevents fraud service outages from blocking all payments.
//
// States:
//   CLOSED  → normal operation, calls fraud service
//   OPEN    → fraud service down, using fallback rules
//   HALF-OPEN → testing if fraud service recovered

package fraud

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

type circuitState int

const (
	stateClosed   circuitState = iota // normal — calls fraud service
	stateOpen                         // tripped — using fallback
	stateHalfOpen                     // testing recovery
)

const (
	failureThreshold = 5                // failures before opening
	openDuration     = 30 * time.Second // how long to stay open
	halfOpenProbes   = 3                // successful probes before closing
)

// CircuitBreakerClient wraps the fraud client with circuit breaker logic.
type CircuitBreakerClient struct {
	client     Client
	logger     *zap.Logger
	mu         sync.Mutex
	state      circuitState
	failures   int
	successes  int
	lastFailed time.Time
}

func NewCircuitBreakerClient(client Client, logger *zap.Logger) *CircuitBreakerClient {
	return &CircuitBreakerClient{
		client: client,
		logger: logger,
		state:  stateClosed,
	}
}

// Check calls the fraud service with circuit breaker protection.
// Falls back to simple rules if the circuit is open.
func (cb *CircuitBreakerClient) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	cb.mu.Lock()
	state := cb.currentState()
	cb.mu.Unlock()

	switch state {
	case stateOpen:
		cb.logger.Warn("fraud service circuit OPEN — using fallback rules",
			zap.String("transaction_id", req.TransactionID.String()),
		)
		return cb.fallback(req), nil

	case stateHalfOpen:
		cb.logger.Info("fraud service circuit HALF-OPEN — probing")
		resp, err := cb.client.Check(ctx, req)
		cb.mu.Lock()
		if err != nil {
			cb.recordFailure()
		} else {
			cb.recordSuccess()
		}
		cb.mu.Unlock()
		if err != nil {
			return cb.fallback(req), nil
		}
		return resp, nil

	default: // stateClosed
		resp, err := cb.client.Check(ctx, req)
		cb.mu.Lock()
		if err != nil {
			cb.recordFailure()
			cb.logger.Error("fraud service call failed",
				zap.Error(err),
				zap.Int("failures", cb.failures),
			)
		} else {
			cb.recordSuccess()
		}
		cb.mu.Unlock()
		if err != nil {
			// First failure — use fallback for this request
			return cb.fallback(req), nil
		}
		return resp, nil
	}
}

func (cb *CircuitBreakerClient) RecordApproved(ctx context.Context, req CheckRequest) error {
	// Best effort — fire and forget
	// Don't block the payment if this fails
	go func() {
		_ = cb.client.RecordApproved(context.Background(), req) //nolint:errcheck
	}()
	return nil
}

// fallback applies simple rules when the fraud service is unavailable.
// Conservative: allow most transfers, block obvious fraudulent patterns.
func (cb *CircuitBreakerClient) fallback(req CheckRequest) *CheckResponse {
	// Block if same wallet
	if req.SenderWalletID == req.ReceiverWalletID {
		return &CheckResponse{
			Decision:  DecisionBlock,
			RiskScore: 100,
			Reasons:   []string{"fallback: same wallet"},
		}
	}

	// Block if amount exceeds ₦5M
	if req.Amount > 500_000_000 {
		return &CheckResponse{
			Decision:  DecisionBlock,
			RiskScore: 100,
			Reasons:   []string{"fallback: exceeds absolute maximum"},
		}
	}

	// Allow everything else with a medium risk flag
	return &CheckResponse{
		Decision:  DecisionAllow,
		RiskScore: 30,
		Reasons:   []string{"fallback rules applied — fraud service unavailable"},
	}
}

func (cb *CircuitBreakerClient) currentState() circuitState {
	if cb.state == stateOpen {
		if time.Since(cb.lastFailed) > openDuration {
			cb.state = stateHalfOpen
			cb.successes = 0
		}
	}
	return cb.state
}

func (cb *CircuitBreakerClient) recordFailure() {
	cb.failures++
	cb.successes = 0
	cb.lastFailed = time.Now()
	if cb.failures >= failureThreshold {
		cb.state = stateOpen
		cb.logger.Warn("fraud service circuit OPENED",
			zap.Int("failures", cb.failures),
		)
	}
}

func (cb *CircuitBreakerClient) recordSuccess() {
	cb.failures = 0
	if cb.state == stateHalfOpen {
		cb.successes++
		if cb.successes >= halfOpenProbes {
			cb.state = stateClosed
			cb.logger.Info("fraud service circuit CLOSED — service recovered")
		}
	}
}
