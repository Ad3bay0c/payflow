// internal/domain/fee_test.go
package domain

import "testing"

func TestCalculateTransferFee(t *testing.T) {
	tests := []struct {
		name        string
		amountKobo  int64
		expectedFee int64
	}{
		{"minimum transfer", 100, 1000},           // ₦1 → ₦10 fee
		{"small transfer boundary", 500000, 1000}, // ₦5,000 → ₦10 fee
		{"medium transfer", 500001, 2500},         // ₦5,000.01 → ₦25 fee
		{"medium boundary", 5000000, 2500},        // ₦50,000 → ₦25 fee
		{"large transfer", 5000001, 5000},         // ₦50,000.01 → ₦50 fee
		{"very large transfer", 100000000, 5000},  // ₦1,000,000 → ₦50 fee (capped)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee := CalculateTransferFee(tt.amountKobo)
			if fee != tt.expectedFee {
				t.Errorf("amount %d kobo: expected fee %d, got %d",
					tt.amountKobo, tt.expectedFee, fee)
			}
		})
	}
}

func TestCalculateTotalDebit(t *testing.T) {
	amount := int64(1000000) // ₦10,000
	total, fee := CalculateTotalDebit(amount)

	if fee != 2500 {
		t.Errorf("expected fee 2500 kobo, got %d", fee)
	}
	if total != 1002500 {
		t.Errorf("expected total 1002500 kobo, got %d", total)
	}
}
