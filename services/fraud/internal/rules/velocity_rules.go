// internal/rules/velocity_rules.go
//
// Velocity rules — check how frequently a wallet is transacting.
// Uses Redis for fast counters. Target latency: < 5ms per rule.

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
	// Maximum transfers per hour before flagging
	maxTransfersPerHour = 10

	// Maximum amount transferred in 24 hours (in kobo) — ₦500,000
	maxDailyAmountKobo = 50_000_000

	// Maximum failed attempts in 30 minutes before blocking
	maxFailedAttemptsPerHour = 5

	fraudVelocityDailyAmountKey = "fraud:velocity:daily_amount"
	fraudVelocityHourlyCountKey = "fraud:velocity:hourly_count"
)

// HourlyTransferCountRule checks how many transfers
// the sender has made in the last hour.
type HourlyTransferCountRule struct {
	redis *redis.Client
}

func NewHourlyTransferCountRule(redisClient *redis.Client) *HourlyTransferCountRule {
	return &HourlyTransferCountRule{redis: redisClient}
}

func (r *HourlyTransferCountRule) Name() string { return "hourly_transfer_count" }

func (r *HourlyTransferCountRule) Evaluate(ctx context.Context, req domain.CheckRequest) domain.RuleResult {
	key := fmt.Sprintf("%s:%s", fraudVelocityHourlyCountKey, req.SenderWalletID)

	count, err := r.redis.Get(ctx, key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		// Redis error — skip rule, don't block payments due to infrastructure issues
		return domain.RuleResult{RuleName: r.Name()}
	}

	if count >= maxTransfersPerHour {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     50,
			Triggered: true,
			Reason:    fmt.Sprintf("high transfer frequency: %d transfers in the last hour", count),
		}
	}

	if count >= maxTransfersPerHour/2 {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     20,
			Triggered: true,
			Reason:    fmt.Sprintf("elevated transfer frequency: %d transfers in the last hour", count),
		}
	}

	return domain.RuleResult{RuleName: r.Name()}
}

// DailyAmountRule checks how much the sender has transferred today.
type DailyAmountRule struct {
	redis *redis.Client
}

func NewDailyAmountRule(redisClient *redis.Client) *DailyAmountRule {
	return &DailyAmountRule{redis: redisClient}
}

func (r *DailyAmountRule) Name() string { return "daily_amount_velocity" }

func (r *DailyAmountRule) Evaluate(ctx context.Context, req domain.CheckRequest) domain.RuleResult {
	key := fmt.Sprintf("%s:%s", fraudVelocityDailyAmountKey, req.SenderWalletID)

	total, err := r.redis.Get(ctx, key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return domain.RuleResult{RuleName: r.Name()}
	}

	// Including this transfer — would we exceed the daily limit?
	projected := total + req.Amount

	if projected > maxDailyAmountKobo {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     60,
			Triggered: true,
			Reason:    fmt.Sprintf("daily transfer limit approaching: ₦%.2f transferred today", float64(total)/100),
		}
	}

	return domain.RuleResult{RuleName: r.Name()}
}

// RecordTransfer updates velocity counters after a successful transfer.
// Called by the fraud service AFTER a payment is approved and completed.
func RecordTransfer(ctx context.Context, redisClient *redis.Client, req domain.CheckRequest) {
	// Hourly transfer count
	hourlyKey := fmt.Sprintf("%s:%s", fraudVelocityHourlyCountKey, req.SenderWalletID)
	pipe := redisClient.Pipeline()
	pipe.Incr(ctx, hourlyKey)
	pipe.Expire(ctx, hourlyKey, time.Hour)

	// Daily amount total
	dailyKey := fmt.Sprintf("%s:%s", fraudVelocityDailyAmountKey, req.SenderWalletID)
	pipe.IncrBy(ctx, dailyKey, req.Amount)
	pipe.Expire(ctx, dailyKey, 24*time.Hour)

	pipe.Exec(ctx) //nolint:errcheck
}

// DefaultVelocityRules returns velocity rules configured with a Redis client.
func DefaultVelocityRules(redisClient *redis.Client) []Rule {
	return []Rule{
		NewHourlyTransferCountRule(redisClient),
		NewDailyAmountRule(redisClient),
	}
}
