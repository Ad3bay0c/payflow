// internal/lookup/user_lookup.go

package lookup

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/Ad3bay0c/payflow/notification/internal/service"
	authpb "github.com/Ad3bay0c/payflow/proto/gen/auth"
)

var _ service.UserLookup = (*GRPCUserLookup)(nil)

// GRPCUserLookup resolves wallet IDs to user contact details
// using gRPC calls to the auth service.
type GRPCUserLookup struct {
	authClient         authpb.AuthServiceClient
	authConn           *grpc.ClientConn
	walletUserResolver WalletUserResolver
}

func NewGRPCUserLookup(
	authServiceAddr string,
	resolver WalletUserResolver,
) (*GRPCUserLookup, error) {
	conn, err := grpc.NewClient(authServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to auth service: %w", err)
	}

	return &GRPCUserLookup{
		authClient:         authpb.NewAuthServiceClient(conn),
		authConn:           conn,
		walletUserResolver: resolver,
	}, nil
}

// GetPhoneByWalletID resolves a wallet ID to a user's phone number.
// - wallet_id → user_id (via payment service HTTP — will be gRPC in Phase 4)
// - user_id → phone_number (via auth service gRPC)
func (l *GRPCUserLookup) GetPhoneByWalletID(ctx context.Context, walletID string) (uuid.UUID, string, error) {
	userIDStr, err := l.walletUserResolver.GetUserIDByWalletID(ctx, walletID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("resolving wallet to user: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	resp, err := l.authClient.GetPhoneByUserID(ctx, &authpb.GetPhoneByUserIDRequest{
		UserId: userIDStr,
	})
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("getting phone from auth service: %w", err)
	}

	userID, err := uuid.Parse(resp.UserId)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("invalid user id in response: %w", err)
	}

	return userID, resp.PhoneNumber, nil
}

func (l *GRPCUserLookup) Close() error {
	return l.authConn.Close()
}
