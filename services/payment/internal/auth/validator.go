// internal/auth/validator.go
//
// Client for validating tokens against the auth service.
// Called only for high-value operations — not on every request.
// For routine requests, local JWT signature verification is sufficient.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TokenValidator calls the auth service to verify a token
// is both cryptographically valid AND not in the revocation list.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (*ValidationResult, error)
}

type ValidationResult struct {
	Valid     bool
	UserID    string
	Tier      int16
	KYCStatus string
}

type httpTokenValidator struct {
	authServiceURL string
	httpClient     *http.Client
}

func NewTokenValidator(authServiceURL string) TokenValidator {
	return &httpTokenValidator{
		authServiceURL: authServiceURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second, // strict timeout — never block a payment long
		},
	}
}

func (v *httpTokenValidator) Validate(ctx context.Context, token string) (*ValidationResult, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		v.authServiceURL+"/api/v1/auth/validate-token",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling auth service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return &ValidationResult{Valid: false}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth service returned %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Valid     bool   `json:"valid"`
			UserID    string `json:"user_id"`
			Tier      int16  `json:"tier"`
			KYCStatus string `json:"kyc_status"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &ValidationResult{
		Valid:     result.Data.Valid,
		UserID:    result.Data.UserID,
		Tier:      result.Data.Tier,
		KYCStatus: result.Data.KYCStatus,
	}, nil
}
