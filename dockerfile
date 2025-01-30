# BUILD STAGE FOR UI
FROM node:20-alpine AS ui-builder
WORKDIR /app/web
COPY web/ .
RUN npm ci
RUN npm run build

# BUILD STAGE FOR GO BACKEND
FROM golang:1.22-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev sqlite
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/web/build ./web/build


# BUILD BINARY
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o distroface ./cmd/main.go

# FINAL
FROM alpine:3.19

RUN apk add --no-cache sqlite sqlite-libs ca-certificates tzdata
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

RUN mkdir -p /data/registry /data/db /data/certs && \
    chown -R appuser:appgroup /data

WORKDIR /app

COPY --from=go-builder /app/distroface .
COPY --from=go-builder /app/web/build ./web/build
COPY --from=go-builder /app/db/schema.sql /app/db/schema.sql
COPY --from=go-builder /app/db/initdb.sql /app/db/initdb.sql
COPY --from=go-builder /app/docker.config.yml /app/config.yml

COPY entrypoint.sh /app/
RUN chmod +x /app/entrypoint.sh

RUN chown -R appuser:appgroup /app
USER appuser

EXPOSE 8668

ENV GO_ENV=production \
    TZ=UTC

VOLUME ["/data/registry", "/data/db", "/data/certs"]

# HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
#     CMD wget --no-verbose --tries=1 --spider http://0.0.0.0:${PORT}/v2/ || exit 1


ENTRYPOINT ["/app/entrypoint.sh"]
