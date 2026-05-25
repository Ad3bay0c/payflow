// internal/fraud/client.go
//
// gRPC client for the fraud service.

package fraud

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	fraudpb "github.com/Ad3bay0c/payflow/proto/gen/fraud"
)

// Decision mirrors the proto Decision values.
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionBlock Decision = "BLOCK"
	DecisionFlag  Decision = "FLAG"
)

// CheckRequest is what the payment service sends to fraud.
// Mirrors the proto CheckRequest — kept separate so payment service
// domain types don't depend on proto types directly.
type CheckRequest struct {
	TransactionID    string
	SenderWalletID   string
	ReceiverWalletID string
	SenderUserID     string
	Amount           int64
	Currency         string
	SenderTier       int32
	SenderKYCStatus  string
	IPAddress        string
	DeviceID         string
	RequestedAt      time.Time
}

// CheckResponse is what the fraud service returns.
type CheckResponse struct {
	Decision  Decision
	RiskScore int32
	RiskLevel string
	Reasons   []string
	LatencyMs int64
}

// Client is the interface for fraud service communication.
type Client interface {
	Check(ctx context.Context, req CheckRequest) (*CheckResponse, error)
	RecordApproved(ctx context.Context, req CheckRequest) error
	Close() error
}

type grpcClient struct {
	conn   *grpc.ClientConn
	client fraudpb.FraudServiceClient
}

// NewGRPCClient creates a new gRPC client connected to the fraud service.
func NewGRPCClient(addr string) (Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()), // TLS in production via mTLS
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to fraud service: %w", err)
	}

	return &grpcClient{
		conn:   conn,
		client: fraudpb.NewFraudServiceClient(conn),
	}, nil
}

func (c *grpcClient) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	// Apply strict timeout — fraud check must complete within 80ms
	ctx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()

	resp, err := c.client.Check(ctx, &fraudpb.CheckRequest{
		TransactionId:    req.TransactionID,
		SenderWalletId:   req.SenderWalletID,
		ReceiverWalletId: req.ReceiverWalletID,
		SenderUserId:     req.SenderUserID,
		AmountKobo:       req.Amount,
		Currency:         req.Currency,
		SenderTier:       req.SenderTier,
		SenderKycStatus:  req.SenderKYCStatus,
		IpAddress:        req.IPAddress,
		DeviceId:         req.DeviceID,
		RequestedAt:      req.RequestedAt.Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("fraud check rpc: %w", err)
	}

	return &CheckResponse{
		Decision:  Decision(resp.Decision),
		RiskScore: resp.RiskScore,
		RiskLevel: resp.RiskLevel,
		Reasons:   resp.Reasons,
		LatencyMs: resp.LatencyMs,
	}, nil
}

func (c *grpcClient) RecordApproved(ctx context.Context, req CheckRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.client.RecordApproved(ctx, &fraudpb.CheckRequest{
		TransactionId:    req.TransactionID,
		SenderWalletId:   req.SenderWalletID,
		ReceiverWalletId: req.ReceiverWalletID,
		SenderUserId:     req.SenderUserID,
		AmountKobo:       req.Amount,
		Currency:         req.Currency,
		SenderTier:       req.SenderTier,
		SenderKycStatus:  req.SenderKYCStatus,
		RequestedAt:      req.RequestedAt.Format(time.RFC3339),
	})
	return err
}

func (c *grpcClient) Close() error {
	return c.conn.Close()
}
