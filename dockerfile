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
  FROM --platform=$TARGETPLATFORM golang:1.22-bullseye AS go-builder

  WORKDIR /app
  
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
  
  RUN CGO_ENABLED=1 go build -a -installsuffix cgo -o distroface ./cmd/distroface/main.go

# ---------------------------
# 3) FINAL STAGE
# ---------------------------
  FROM --platform=$TARGETPLATFORM debian:bookworm-slim

  ENV DEBIAN_FRONTEND=noninteractive

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
  
  # CREATE MNT DIRS
  RUN mkdir -p /data/registry /data/db /data/certs && \
      chown -R appuser:appgroup /data
  
  WORKDIR /app
  
  # BRING IN UI + GO BINARY
  COPY --from=ui-builder /app/web/build ./web/build
  COPY --from=go-builder /app/distroface .
  COPY --from=go-builder /app/docker.config.yml config.yml

  RUN chown -R appuser:appgroup /app
  
  USER appuser
  
  EXPOSE 8668
  ENV GO_ENV=production \
      TZ=UTC
  
  ENTRYPOINT ["./distroface"]
