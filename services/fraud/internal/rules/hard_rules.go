// internal/rules/hard_rules.go
//
// Hard rules — instant evaluation, no I/O.
// These run first and are cheapest to evaluate.
// A triggered hard rule adds the maximum score immediately.

package rules

import (
	"context"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
)

const (
	// Absolute maximum single transfer — regardless of tier
	absoluteMaxTransferKobo = 500_000_000 // ₦5,000,000

	// Unusual hours — late night transfers are higher risk
	unusualHourStart = 1 // 1am
	unusualHourEnd   = 5 // 5am
)

// SameWalletRule blocks transfers where sender equals receiver.
// This should be caught by the payment service too — defence in depth.
type SameWalletRule struct{}

func (r *SameWalletRule) Name() string { return "same_wallet" }

func (r *SameWalletRule) Evaluate(_ context.Context, req domain.CheckRequest) domain.RuleResult {
	if req.SenderWalletID == req.ReceiverWalletID {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     100,
			Triggered: true,
			Reason:    "sender and receiver wallet are the same",
		}
	}
	return domain.RuleResult{RuleName: r.Name()}
}

// AbsoluteAmountRule blocks transfers exceeding the absolute maximum.
type AbsoluteAmountRule struct{}

func (r *AbsoluteAmountRule) Name() string { return "absolute_amount_limit" }

func (r *AbsoluteAmountRule) Evaluate(_ context.Context, req domain.CheckRequest) domain.RuleResult {
	if req.Amount > absoluteMaxTransferKobo {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     100,
			Triggered: true,
			Reason:    "transfer amount exceeds absolute maximum limit",
		}
	}
	return domain.RuleResult{RuleName: r.Name()}
}

// UnverifiedKYCHighAmountRule flags large transfers from unverified accounts.
// A Tier 1 user sending ₦40,000 in one transfer is suspicious —
// they should be Tier 2 for amounts that high.
type UnverifiedKYCHighAmountRule struct{}

func (r *UnverifiedKYCHighAmountRule) Name() string { return "unverified_kyc_high_amount" }

func (r *UnverifiedKYCHighAmountRule) Evaluate(_ context.Context, req domain.CheckRequest) domain.RuleResult {
	// Tier 1 users sending more than ₦30,000 in one transfer
	if req.SenderTier == 1 && req.Amount > 3_000_000 {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     40,
			Triggered: true,
			Reason:    "high amount transfer from unverified (Tier 1) account",
		}
	}
	return domain.RuleResult{RuleName: r.Name()}
}

// UnusualHourRule flags transfers during unusual hours (1am–5am).
// Not a block — just adds to the risk score.
type UnusualHourRule struct{}

func (r *UnusualHourRule) Name() string { return "unusual_hour" }

func (r *UnusualHourRule) Evaluate(_ context.Context, req domain.CheckRequest) domain.RuleResult {
	hour := req.RequestedAt.UTC().Hour()
	if hour >= unusualHourStart && hour < unusualHourEnd {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     15,
			Triggered: true,
			Reason:    "transfer initiated during unusual hours (2am–5am UTC)",
		}
	}
	return domain.RuleResult{RuleName: r.Name()}
}

// RoundAmountRule flags suspiciously round amounts.
// Fraudsters often test cards/wallets with round numbers like ₦1,000, ₦5,000, ₦10,000.
type RoundAmountRule struct{}

func (r *RoundAmountRule) Name() string { return "round_amount" }

func (r *RoundAmountRule) Evaluate(_ context.Context, req domain.CheckRequest) domain.RuleResult {
	// Amount is a round number of naira (divisible by 10,000 kobo = ₦100)
	// and above ₦10,000
	if req.Amount >= 1_000_000 && req.Amount%1_000_000 == 0 {
		return domain.RuleResult{
			RuleName:  r.Name(),
			Score:     10,
			Triggered: true,
			Reason:    "suspiciously round transfer amount",
		}
	}
	return domain.RuleResult{RuleName: r.Name()}
}

// ensure RoundAmountRule implements Rule
var _ Rule = (*RoundAmountRule)(nil)
var _ Rule = (*SameWalletRule)(nil)
var _ Rule = (*AbsoluteAmountRule)(nil)
var _ Rule = (*UnverifiedKYCHighAmountRule)(nil)
var _ Rule = (*UnusualHourRule)(nil)

// DefaultHardRules returns the standard set of hard rules in evaluation order.
func DefaultHardRules() []Rule {
	return []Rule{
		&SameWalletRule{},
		&AbsoluteAmountRule{},
		&UnverifiedKYCHighAmountRule{},
		&UnusualHourRule{},
		&RoundAmountRule{},
	}
}
