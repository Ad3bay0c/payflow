// internal/lookup/user_lookup.go

package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type authServiceLookup struct {
	authServiceURL    string
	paymentServiceURL string
	adminKey          string
	httpClient        *http.Client
}

func NewAuthServiceLookup(authServiceURL, paymentServiceURL, adminKey string) *authServiceLookup {
	return &authServiceLookup{
		authServiceURL:    authServiceURL,
		paymentServiceURL: paymentServiceURL,
		adminKey:          adminKey,
		httpClient:        &http.Client{Timeout: 3 * time.Second},
	}
}

// GetPhoneByWalletID fetches user contact details for a wallet.
// 1: payment service → get user_id from wallet_id
// 2: auth service → get phone from user_id
func (l *authServiceLookup) GetPhoneByWalletID(ctx context.Context, walletID string) (uuid.UUID, string, error) {
	// get user_id from payment service
	userID, err := l.getUserIDFromWallet(ctx, walletID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("getting user from wallet: %w", err)
	}

	// get phone from auth service
	phone, err := l.getPhoneFromAuth(ctx, userID.String())
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("getting phone from auth: %w", err)
	}

	return userID, phone, nil
}

func (l *authServiceLookup) getUserIDFromWallet(ctx context.Context, walletID string) (uuid.UUID, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/wallets/%s/owner", l.paymentServiceURL, walletID),
		nil,
	)
	if err != nil {
		return uuid.Nil, err
	}
	req.Header.Set("X-Admin-Key", l.adminKey)

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return uuid.Nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return uuid.Nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return uuid.Nil, err
	}

	var result struct {
		Data struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return uuid.Nil, err
	}

	return uuid.Parse(result.Data.UserID)
}

func (l *authServiceLookup) getPhoneFromAuth(ctx context.Context, userID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/users/%s", l.authServiceURL, userID),
		nil,
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Admin-Key", l.adminKey)

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			PhoneNumber string `json:"phone_number"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Data.PhoneNumber, nil
}
