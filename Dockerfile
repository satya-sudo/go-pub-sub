# ---------- Builder Stage ----------
FROM golang:1.25-alpine AS builder

# Install tools and CA certs for module download
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy go module files first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy full project
COPY . .

# Build the broker binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/gopubsub ./cmd/server

# ---------- Runtime Stage ----------
FROM alpine:3.20

# Security: create non-root user
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates

# Copy binary & config
COPY --from=builder /app/gopubsub /usr/local/bin/gopubsub
COPY config/config.yaml /etc/gopubsub/config.yaml

EXPOSE 50051

USER app

# Default command — uses baked config (override at runtime if needed)
ENTRYPOINT ["/usr/local/bin/gopubsub", "--config=/etc/gopubsub/config.yaml"]
