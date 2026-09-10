# --- Build stage ---
FROM golang:1.26-alpine AS builder

# CN mirrors (override via build args when building outside China):
# GOPROXY for Go modules/toolchains, tuna mirror for Alpine packages.
ARG GOPROXY_MIRROR=https://goproxy.cn,direct
ARG APK_MIRROR=mirrors.tuna.tsinghua.edu.cn
ENV GOPROXY=${GOPROXY_MIRROR}
RUN sed -i "s#https://dl-cdn.alpinelinux.org#https://${APK_MIRROR}#" /etc/apk/repositories && \
    apk add --no-cache git

WORKDIR /src

# Cache dependencies
COPY server/go.mod server/go.sum ./server/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd server && go mod download

# Copy server source
COPY server/ ./server/

# Build binaries. Cache mounts persist the module cache and the compiled
# object cache across builds, so only changed packages recompile.
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd server && \
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o bin/server ./cmd/server && \
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o bin/multica ./cmd/multica && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/backfill_task_usage_hourly ./cmd/backfill_task_usage_hourly && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/backfill_codex_usage_cache ./cmd/backfill_codex_usage_cache

# --- Runtime stage ---
FROM alpine:3.21

ARG APK_MIRROR=mirrors.tuna.tsinghua.edu.cn
RUN sed -i "s#https://dl-cdn.alpinelinux.org#https://${APK_MIRROR}#" /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /src/server/bin/server .
COPY --from=builder /src/server/bin/multica .
COPY --from=builder /src/server/bin/migrate .
COPY --from=builder /src/server/bin/backfill_task_usage_hourly .
COPY --from=builder /src/server/bin/backfill_codex_usage_cache .
COPY server/migrations/ ./migrations/
COPY LICENSE NOTICE ./
COPY docker/entrypoint.sh .
RUN sed -i 's/\r$//' entrypoint.sh && chmod +x entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["./entrypoint.sh"]
