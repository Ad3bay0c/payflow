// internal/lookup/wallet_resolver.go

package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WalletUserResolver resolves a wallet ID to a user ID.
// Implemented by calling the payment service internal API.
type WalletUserResolver interface {
	GetUserIDByWalletID(ctx context.Context, walletID string) (string, error)
}

// TODO: implement GRPC client to connect to Payment service
type HTTPWalletResolver struct {
	paymentServiceURL string
	adminKey          string
	httpClient        *http.Client
}

func NewHTTPWalletResolver(paymentServiceURL, adminKey string) *HTTPWalletResolver {
	return &HTTPWalletResolver{
		paymentServiceURL: paymentServiceURL,
		adminKey:          adminKey,
		httpClient:        &http.Client{Timeout: 3 * time.Second},
	}
}

func (r *HTTPWalletResolver) GetUserIDByWalletID(ctx context.Context, walletID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/wallets/%s/owner", r.paymentServiceURL, walletID),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("X-Admin-Key", r.adminKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling payment service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("payment service returned %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if result.Data.UserID == "" {
		return "", fmt.Errorf("empty user_id in response")
	}

	return result.Data.UserID, nil
}
