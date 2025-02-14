# syntax=docker/dockerfile:1.4
#
# Multi-arch Dockerfile for Go + Svelte + SQLite

# ---------------------------
# 1) BUILD STAGE FOR UI
# ---------------------------
FROM --platform=$BUILDPLATFORM node:20-bullseye AS ui-builder

WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build


# ---------------------------
# 2) BUILD STAGE FOR GO BACKEND
# ---------------------------
FROM --platform=$BUILDPLATFORM golang:1.22-bullseye AS go-builder

WORKDIR /app

# Install build dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        gcc \
        libc6-dev \
        pkg-config \
        libsqlite3-dev && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download
COPY . .

# BRING IN UI AND BUILD GO APP
COPY --from=ui-builder /app/web/build ./web/build

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -a -installsuffix cgo -o distroface ./cmd/distroface/main.go


# ---------------------------
# 3) FINAL STAGE
# ---------------------------
FROM --platform=$TARGETPLATFORM debian:bullseye-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        libsqlite3-0 \
        sqlite3 && \
    rm -rf /var/lib/apt/lists/*

# NON ROOT 1000:1000
RUN groupadd -r -g 1000 appgroup && \
    useradd -r -u 1000 -g appgroup -d /app appuser

# MAKE MNT DIRS
RUN mkdir -p /data/registry /data/db /data/certs && \
    chown -R appuser:appgroup /data

WORKDIR /app

# COPY GO BINARY + WEB PACK
COPY --from=go-builder /app/distroface .
COPY --from=go-builder /app/web/build ./web/build
COPY --from=go-builder /app/docker.config.yml /app/config.yml

# SET OWNER
RUN chown -R appuser:appgroup /app

USER appuser
EXPOSE 8668

ENV GO_ENV=production \
    TZ=UTC

ENTRYPOINT ["./distroface"]
