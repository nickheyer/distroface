# syntax=docker/dockerfile:1.4
#
# Multi-arch Dockerfile for Go + Svelte + SQLite

# ---------------------------
# 1) BUILD STAGE FOR UI
# ---------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS ui-builder

WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build


# ---------------------------
# 2) BUILD STAGE FOR GO BACKEND
# ---------------------------
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS go-builder

WORKDIR /app

RUN apk add --no-cache gcc musl-dev sqlite-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# BRING IN UI AND BUILD GO APP
COPY --from=ui-builder /app/web/build ./web/build
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -installsuffix cgo -o distroface ./cmd/distroface/main.go


# ---------------------------
# 3) FINAL STAGE
# ---------------------------
FROM alpine:3.19

RUN apk add --no-cache \
    sqlite \
    sqlite-libs \
    ca-certificates \
    tzdata && \
    addgroup -S -g 1000 appgroup && \
    adduser -S -u 1000 -G appgroup -h /app appuser && \
    mkdir -p /data/registry /data/db /data/certs && \
    chown -R appuser:appgroup /data

WORKDIR /app
COPY --from=go-builder /app/distroface .
COPY --from=go-builder /app/web/build ./web/build
COPY --from=go-builder /app/docker.config.yml /app/config.yml
RUN chown -R appuser:appgroup /app

USER appuser
EXPOSE 8668

ENV GO_ENV=production \
    TZ=UTC

ENTRYPOINT ["./distroface"]
