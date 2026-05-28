# Stage Builder
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go workspace files first
COPY go.work go.work.sum ./

# Copy all module go.mod and go.sum files
# This allows Docker to cache dependency downloads separately
# from source code changes
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY proto/go.mod proto/go.sum ./proto/
COPY services/auth/go.mod services/auth/go.sum ./services/auth/
COPY services/payment/go.mod services/payment/go.sum ./services/payment/
COPY services/fraud/go.mod services/fraud/go.sum ./services/fraud/
COPY services/ledger/go.mod services/ledger/go.sum ./services/ledger/
COPY services/notification/go.mod services/notification/go.sum ./services/notification/

# Download dependencies
RUN cd services/auth && go mod download

# Copy source code
COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/auth/ ./services/auth/

# Build the binary
# CGO_ENABLED=0 — pure Go binary, no C dependencies
# GOOS=linux — target Linux (even if building on Mac)
# -ldflags="-w -s" — strip debug info, reduces binary size ~30%
# -trimpath — remove local file paths from binary (security + reproducibility)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -trimpath \
    -o /bin/auth-service \
    ./services/auth/cmd/server

# Stage Final
FROM gcr.io/distroless/static-debian12:nonroot

# Copy timezone data from builder (needed for time.LoadLocation)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy CA certificates (needed for HTTPS calls e.g. Termii SMS)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /bin/auth-service /auth-service

USER nonroot:nonroot

# 8081 - HTTP Server
# 9091 - GRPC Server
EXPOSE 8081 9091

# Health check — Kubernetes liveness probe
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/auth-service", "-health-check"] || exit 1

ENTRYPOINT ["/auth-service"]
