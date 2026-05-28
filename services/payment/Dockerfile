# Stage: Builder
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.work go.work.sum ./
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY proto/go.mod proto/go.sum ./proto/
COPY services/auth/go.mod services/auth/go.sum ./services/auth/
COPY services/payment/go.mod services/payment/go.sum ./services/payment/
COPY services/fraud/go.mod services/fraud/go.sum ./services/fraud/
COPY services/ledger/go.mod services/ledger/go.sum ./services/ledger/
COPY services/notification/go.mod services/notification/go.sum ./services/notification/

RUN cd services/payment && go mod download

COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/payment/ ./services/payment/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -trimpath \
    -o /bin/payment-service \
    ./services/payment/cmd/server

# Stage: Final
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/payment-service /payment-service

USER nonroot:nonroot

# 8082 - HTTP Server
# 8083 - Admin Server
# 9093 - GRPC Server
EXPOSE 8082 8083 9092

ENTRYPOINT ["/payment-service"]
