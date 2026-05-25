// internal/rules/pattern_rules.go
//
// Pattern rules — detect unusual behaviour relative to history.
// These are the most expensive rules
// Target latency: < 30ms per rule.

package rules

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
)

const (
	fraudKnownReceiverKey = "fraud:known_receiver"
	fraudRapidKey         = "fraud:rapid"
)

// NewReceiverRule flags transfers to wallets the sender
// has never sent to before. First-time recipients are higher risk.
type NewReceiverRule struct {
	redis *redis.Client
}

func NewNewReceiverRule(redisClient *redis.Client) *NewReceiverRule {
	return &NewReceiverRule{redis: redisClient}
}

func (r *NewReceiverRule) Name() string { return "new_receiver" }

func (r *NewReceiverRule) Evaluate(ctx context.Context, req domain.CheckRequest) domain.RuleResult {
	// Check if sender has sent to this receiver before
	key := fmt.Sprintf("%s:%s:%s",
		fraudKnownReceiverKey,
		req.SenderWalletID,
		req.ReceiverWalletID,
	)

	exists, err := r.redis.Exists(ctx, key).Result()
	if err != nil {
		return domain.RuleResult{RuleName: r.Name()}
	}

	if exists == 0 {
		// First time sending to this receiver
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     15,
			Triggered: true,
			Reason:    "first transfer to this recipient",
		}
	}

	return domain.RuleResult{RuleName: r.Name()}
}

// RecordKnownReceiver marks a sender-receiver pair as known.
// Called after a successful transfer — future transfers to same
// receiver will not trigger the new_receiver rule.
func RecordKnownReceiver(ctx context.Context, redisClient *redis.Client, req domain.CheckRequest) {
	key := fmt.Sprintf("%s:%s:%s",
		fraudKnownReceiverKey,
		req.SenderWalletID,
		req.ReceiverWalletID,
	)
	// Store for 90 days — if no transfer in 90 days, treat as new again
	redisClient.Set(ctx, key, "1", 90*24*time.Hour) //nolint:errcheck
}

// RapidSuccessionRule detects multiple transfers in very quick succession.
// More than 3 transfers from the same wallet in 5 minutes is suspicious.
type RapidSuccessionRule struct {
	redis *redis.Client
}

func NewRapidSuccessionRule(redisClient *redis.Client) *RapidSuccessionRule {
	return &RapidSuccessionRule{redis: redisClient}
}

func (r *RapidSuccessionRule) Name() string { return "rapid_succession" }

func (r *RapidSuccessionRule) Evaluate(ctx context.Context, req domain.CheckRequest) domain.RuleResult {
	key := fmt.Sprintf("%s:%s", fraudRapidKey, req.SenderWalletID)

	count, err := r.redis.Get(ctx, key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return domain.RuleResult{RuleName: r.Name()}
	}

	if count >= 3 {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     35,
			Triggered: true,
			Reason:    fmt.Sprintf("rapid succession: %d transfers in the last 5 minutes", count),
		}
	}

	return domain.RuleResult{RuleName: r.Name()}
}

// RecordRapidTransfer records a transfer for rapid succession detection.
func RecordRapidTransfer(ctx context.Context, redisClient *redis.Client, req domain.CheckRequest) {
	key := fmt.Sprintf("%s:%s", fraudRapidKey, req.SenderWalletID)
	pipe := redisClient.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 5*time.Minute)
	pipe.Exec(ctx) //nolint:errcheck
}

// DefaultPatternRules returns pattern rules configured with a Redis client.
func DefaultPatternRules(redisClient *redis.Client) []Rule {
	return []Rule{
		NewNewReceiverRule(redisClient),
		NewRapidSuccessionRule(redisClient),
	}
}
