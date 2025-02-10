# BUILD STAGE FOR UI
FROM node:20-alpine AS ui-builder
WORKDIR /app/web
COPY web/ .
RUN npm ci
RUN npm run build

# BUILD STAGE FOR GO BACKEND
FROM golang:1.22-bullseye AS go-builder

RUN apt-get update && apt-get install -y gcc libc6-dev libsqlite3-dev
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/web/build ./web/build

# BUILD BINARY
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o distroface ./cmd/distroface/main.go

# FINAL
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S -g 1000 appgroup && adduser -S -u 1000 -G appgroup appuser

RUN mkdir -p /data/registry /data/db /data/certs && \
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

ENTRYPOINT ["/app/distroface"]
