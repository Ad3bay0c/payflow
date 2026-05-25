// internal/fraud/client.go
//
// HTTP client for calling the fraud service.
// The payment service calls this synchronously before every transfer.
// Timeout is 80ms — if fraud doesn't respond, the circuit breaker fires.

package fraud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Decision mirrors the fraud service domain types
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionBlock Decision = "BLOCK"
	DecisionFlag  Decision = "FLAG"
)

type CheckRequest struct {
	TransactionID    uuid.UUID `json:"transaction_id"`
	SenderWalletID   uuid.UUID `json:"sender_wallet_id"`
	ReceiverWalletID uuid.UUID `json:"receiver_wallet_id"`
	SenderUserID     uuid.UUID `json:"sender_user_id"`
	Amount           int64     `json:"amount_kobo"`
	Currency         string    `json:"currency"`
	SenderTier       int16     `json:"sender_tier"`
	SenderKYCStatus  string    `json:"sender_kyc_status"`
	IPAddress        string    `json:"ip_address"`
	DeviceID         string    `json:"device_id"`
	RequestedAt      time.Time `json:"requested_at"`
}

type CheckResponse struct {
	Decision  Decision `json:"decision"`
	RiskScore int      `json:"risk_score"`
	Reasons   []string `json:"reasons"`
	LatencyMs int64    `json:"latency_ms"`
}

// Client calls the fraud service HTTP API.
type Client interface {
	Check(ctx context.Context, req CheckRequest) (*CheckResponse, error)
	RecordApproved(ctx context.Context, req CheckRequest) error
}

type httpClient struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

func NewClient(baseURL, serviceKey string) Client {
	return &httpClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		http: &http.Client{
			// Strict 80ms timeout — fraud service must be fast
			Timeout: 80 * time.Millisecond,
		},
	}
}

func (c *httpClient) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/check", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling fraud service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fraud service returned %d", resp.StatusCode)
	}

	var result struct {
		Data CheckResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result.Data, nil
}

func (c *httpClient) RecordApproved(ctx context.Context, req CheckRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/record", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("calling fraud service: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
