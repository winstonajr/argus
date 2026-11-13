# Multi-stage Dockerfile for Argus
FROM golang:alpine AS builder

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
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata libcap su-exec \
    && addgroup -S argus && adduser -S argus -G argus

WORKDIR /app

COPY --from=builder /bin/argus /app/argus
COPY config.example.yaml /app/config.yaml
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

# Grant raw network socket capability for ICMP ping and set entrypoint permissions
RUN setcap cap_net_raw+ep /app/argus \
    && chmod +x /app/docker-entrypoint.sh \
    && mkdir -p /app/data \
    && chown -R argus:argus /app

EXPOSE 8080
VOLUME ["/app/data"]

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["-config", "/app/config.yaml"]
