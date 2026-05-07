// internal/domain/fee.go

package domain

// TotalDebit returns the total amount that leaves the sender's wallet.
func TotalDebit(amount, fee int64) int64 {
	return amount + fee
}

// TierLimit defines transfer restrictions for a KYC tier.
type TierLimit struct {
	Tier            int16
	MaxTransferKobo int64
	DailyLimitKobo  int64
	Description     string
}

// FeeTier defines one band in the transfer fee schedule.
type FeeTier struct {
	ID            int16
	MaxAmountKobo int64 // 0 = no upper bound
	FeeKobo       int64
	Description   string
}
