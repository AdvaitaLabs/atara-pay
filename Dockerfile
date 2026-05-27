# Multi-stage build for the Atara-Pay gateway.
#
#   docker build -t atara-pay:dev .
#
# Produces a small image (~20MB) running a single static-ish binary.
# Build args:
#   GO_VERSION    Go toolchain (default 1.25.5 — matches Makefile GO_VERSION)
#   ATARA_VERSION SemVer baked into the binary (overrides default `dev`)

ARG GO_VERSION=1.25.5

# ── Stage 1: build ───────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

# Then sources.
COPY . .

ARG ATARA_VERSION=dev
ENV CGO_ENABLED=0 GOOS=linux

RUN go build \
        -ldflags="-s -w -X github.com/atara-xyz/atara-pay/cmd/atara/cli.Version=${ATARA_VERSION}" \
        -o /out/atara-pay ./cmd/atara-pay && \
    go build \
        -ldflags="-s -w -X github.com/atara-xyz/atara-pay/cmd/atara/cli.Version=${ATARA_VERSION}" \
        -o /out/atara ./cmd/atara

# ── Stage 2: runtime ─────────────────────────────────────────────────────
FROM alpine:3.20 AS runtime

# Non-root user. The gateway only reads its env and writes to stdout/stderr,
# so a vanilla nobody-style account is sufficient.
RUN addgroup -S atara && adduser -S -G atara atara && \
    apk add --no-cache ca-certificates curl

# Healthcheck dependency.
WORKDIR /app
COPY --from=builder /out/atara-pay /usr/local/bin/atara-pay
COPY --from=builder /out/atara     /usr/local/bin/atara
# Migration files travel with the image so the entrypoint can apply them.
COPY internal/db/migrations /app/migrations

USER atara

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD curl -fsS http://localhost:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/atara-pay"]
