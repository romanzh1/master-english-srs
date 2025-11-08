FROM golang:1.25.3-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o bot ./cmd/bot

FROM debian:bookworm-slim

WORKDIR /app

COPY --from=builder /app/bot .
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/.env ./.env
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

CMD ["./bot"]