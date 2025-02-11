# UI BASE
FROM --platform=${BUILDPLATFORM} node:20-alpine AS ui-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# GO BASE
FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS go-base
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/web/build ./web/build

# AMD-64 BUILD
FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS builder-amd64
WORKDIR /src
RUN apk add --no-cache gcc musl-dev sqlite-dev
COPY --from=go-base /src ./
COPY --from=go-base /go/pkg /go/pkg
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=amd64
RUN go build -ldflags="-w -s" -o distroface ./cmd/distroface/main.go

# ARM-64 BUILD
FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS builder-arm64
WORKDIR /src
RUN apk add --no-cache gcc musl-dev sqlite
COPY --from=go-base /src ./
COPY --from=go-base /go/pkg /go/pkg
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=arm64
ENV CC=aarch64-linux-musl-gcc
RUN go build -ldflags="-w -s" -o distroface ./cmd/distroface/main.go

# RUNTIME FOR AMD64
FROM --platform=linux/amd64 alpine:3.19 AS runtime-amd64
RUN apk add --no-cache ca-certificates sqlite-libs tzdata

# RUNTIME FOR ARM64
FROM --platform=linux/arm64 alpine:3.19 AS runtime-arm64
RUN apk add --no-cache ca-certificates sqlite-libs tzdata

# FINAL STAGE - ARCHITECTURE SPECIFIC
FROM runtime-${TARGETARCH} AS final
RUN addgroup -S -g 1000 appgroup && \
    adduser -S -u 1000 -G appgroup -h /app appuser && \
    mkdir -p /data/registry /data/db /data/certs && \
    chown -R appuser:appgroup /data

WORKDIR /app
COPY --chown=appuser:appgroup docker.config.yml config.yml
COPY --from=ui-builder --chown=appuser:appgroup /app/web/build ./web/build

# BUILDX SHOULD THEORETICALLY DO AN 'IF/ELSE' HERE
COPY --from=builder-amd64 --chown=appuser:appgroup /src/distroface . 
COPY --from=builder-arm64 --chown=appuser:appgroup /src/distroface .

USER appuser
EXPOSE 8668
ENV GO_ENV=production \
    TZ=UTC

ENTRYPOINT ["./distroface"]
