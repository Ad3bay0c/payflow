// internal/grpc/helpers.go

package grpc

import "github.com/google/uuid"

// mustParseUUID parses a UUID string.
// Returns uuid.Nil if the string is empty or invalid —
// the rule engine handles nil UUIDs gracefully.
func mustParseUUID(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}
