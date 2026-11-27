# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS builder

WORKDIR /app
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/fincore-api ./cmd/api

FROM alpine:3.21

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S fincore \
    && adduser -S fincore -G fincore

WORKDIR /app
COPY --from=builder /out/fincore-api /usr/local/bin/fincore-api

USER fincore
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/fincore-api"]
