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

RUN cd services/notification && go mod download

COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/notification/ ./services/notification/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -trimpath \
    -o /bin/notification-service \
    ./services/notification/cmd/server

# Stage: Final
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/notification-service /notification-service

USER nonroot:nonroot

EXPOSE 8085

ENTRYPOINT ["/notification-service"]
