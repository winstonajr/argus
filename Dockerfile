# Multi-stage Dockerfile for Argus
FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /bin/argus ./cmd/argus

# Final runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata libcap su-exec \
    && addgroup -S argus && adduser -S argus -G argus

WORKDIR /app

COPY --from=builder /bin/argus /app/argus
COPY config.example.yaml /app/config.yaml
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh \
    && mkdir -p /app/data \
    && chown -R argus:argus /app \
    && setcap cap_net_raw+ep /app/argus

ENV ARGUS_LANG=pt-BR

EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["-config", "/app/config.yaml"]
