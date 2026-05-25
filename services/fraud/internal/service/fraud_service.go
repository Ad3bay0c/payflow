// internal/service/fraud_service.go

package service

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
	"github.com/Ad3bay0c/payflow/fraud/internal/rules"
)

type FraudService interface {
	Check(ctx context.Context, req domain.CheckRequest) domain.CheckResponse
	RecordApprovedTransfer(ctx context.Context, req domain.CheckRequest)
}

type fraudService struct {
	engine *rules.Engine
	redis  *redis.Client
	logger *zap.Logger
}

func NewFraudService(redisClient *redis.Client, logger *zap.Logger) FraudService {
	// Build the rule set in evaluation order:
	// Hard, Velocity, and Pattern rules
	var allRules []rules.Rule
	allRules = append(allRules, rules.DefaultHardRules()...)
	allRules = append(allRules, rules.DefaultVelocityRules(redisClient)...)
	allRules = append(allRules, rules.DefaultPatternRules(redisClient)...)

	return &fraudService{
		engine: rules.NewEngine(allRules, logger),
		redis:  redisClient,
		logger: logger,
	}
}

// Check evaluates a payment request against all fraud rules.
func (s *fraudService) Check(ctx context.Context, req domain.CheckRequest) domain.CheckResponse {
	return s.engine.Evaluate(ctx, req)
}

// RecordApprovedTransfer updates velocity counters and known receiver list
// after a transfer is approved and completed.
// Called by the payment service after a successful transfer.
func (s *fraudService) RecordApprovedTransfer(ctx context.Context, req domain.CheckRequest) {
	rules.RecordTransfer(ctx, s.redis, req)
	rules.RecordKnownReceiver(ctx, s.redis, req)
	rules.RecordRapidTransfer(ctx, s.redis, req)
}
