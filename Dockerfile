# syntax=docker/dockerfile:1
FROM golang:1.25 AS builder

WORKDIR /src
RUN apt-get update && apt-get install -y git ca-certificates && rm -rf /var/lib/apt/lists/*

# Copy go module files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build server binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/go-pub-sub ./cmd/server

# Final slim image
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bin/go-pub-sub /bin/go-pub-sub

ENV BROKER_PORT=50051
ENV BROKER_STORE=memory
ENV BROKER_DEFAULT_TOPIC=events
ENV POSTGRES_DSN=postgres://postgres:postgres@postgres:5432/gopub?sslmode=disable
ENV REDIS_ADDR=redis:6379
ENV REDIS_PASS=

EXPOSE 50051

ENTRYPOINT ["/bin/go-pub-sub"]
