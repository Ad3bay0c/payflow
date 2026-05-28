// internal/lookup/wallet_resolver.go
//
// gRPC client for resolving wallet IDs to user IDs.
// Calls the payment service gRPC server.

package lookup

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	paymentpb "github.com/Ad3bay0c/payflow/proto/gen/payment"
)

// GRPCWalletResolver resolves wallet IDs to user IDs
// via the payment service gRPC server.
type GRPCWalletResolver struct {
	client paymentpb.PaymentServiceClient
	conn   *grpc.ClientConn
}

func NewGRPCWalletResolver(paymentServiceAddr string) (*GRPCWalletResolver, error) {
	conn, err := grpc.NewClient(paymentServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to payment service: %w", err)
	}

	return &GRPCWalletResolver{
		client: paymentpb.NewPaymentServiceClient(conn),
		conn:   conn,
	}, nil
}

// GetUserIDByWalletID resolves a wallet ID to its owner's user ID.
func (r *GRPCWalletResolver) GetUserIDByWalletID(ctx context.Context, walletID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	resp, err := r.client.GetWalletOwner(ctx, &paymentpb.GetWalletOwnerRequest{
		WalletId: walletID,
	})
	if err != nil {
		return "", fmt.Errorf("getting wallet owner: %w", err)
	}

	if resp.UserId == "" {
		return "", fmt.Errorf("empty user_id in response")
	}

	return resp.UserId, nil
}

func (r *GRPCWalletResolver) Close() error {
	return r.conn.Close()
}
