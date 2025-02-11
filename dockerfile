# BUILD STAGE FOR UI
FROM node:20-alpine AS ui-builder
WORKDIR /app/web
COPY web/ .
RUN npm ci
RUN npm run build

# BUILD STAGE FOR GO BACKEND - AMD64
FROM golang:1.22-bullseye AS go-builder-amd64
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/web/build ./web/build
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o distroface ./cmd/distroface/main.go

# BUILD STAGE FOR GO BACKEND - ARM64
FROM golang:1.22-bullseye AS go-builder-arm64
WORKDIR /app
COPY go.* ./
RUN go mod download
RUN apt-get update && apt-get install -y gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
COPY . .
COPY --from=ui-builder /app/web/build ./web/build
ENV CC=aarch64-linux-gnu-gcc
ENV CXX=aarch64-linux-gnu-g++
RUN CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -o distroface ./cmd/distroface/main.go

# FINAL IMAGE - AMD64
FROM debian:bullseye-slim AS final-amd64
RUN apt-get update && apt-get install -y ca-certificates sqlite3 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=go-builder-amd64 /app/distroface .
COPY --from=go-builder-amd64 /app/web/build ./web/build

# FINAL IMAGE - ARM64
FROM debian:bullseye-slim AS final-arm64
RUN apt-get update && apt-get install -y ca-certificates sqlite3 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=go-builder-arm64 /app/distroface .
COPY --from=go-builder-arm64 /app/web/build ./web/build

# MULTI-PLATFORM TARGET
FROM final-${TARGETARCH} AS final
RUN \
    groupadd --system --gid 1000 appgroup && useradd --system --uid 1000 --gid appgroup --no-create-home appuser && \
    mkdir -p /data/registry /data/db /data/certs && \
    chown -R appuser:appgroup /data && \
    chown -R appuser:appgroup /app
USER appuser
WORKDIR /app
EXPOSE 8668
ENV GO_ENV=production \
    TZ=UTC
COPY docker.config.yml /app/config.yml
ENTRYPOINT ["./distroface"]
