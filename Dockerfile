# Multi-stage Dockerfile for Argus
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/argus ./cmd/argus

# Final runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata libcap \
    && addgroup -S argus && adduser -S argus -G argus

WORKDIR /app

COPY --from=builder /bin/argus /app/argus
COPY config.example.yaml /app/config.yaml

# Grant raw network socket capability for ICMP ping
RUN setcap cap_net_raw+ep /app/argus \
    && mkdir -p /app/data \
    && chown -R argus:argus /app

USER argus

EXPOSE 8080
VOLUME ["/app/data"]

ENTRYPOINT ["/app/argus"]
CMD ["-config", "/app/config.yaml"]
