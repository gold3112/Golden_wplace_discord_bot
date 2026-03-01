# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /app

# gitやビルドに必要なツールをインストール (alpineの場合)
RUN apk add --no-cache git

COPY go.mod go.sum ./
# ネットワークエラー対策でプロキシを明示
ENV GOPROXY=https://proxy.golang.org,direct
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o bot ./cmd/bot

FROM gcr.io/distroless/base-debian12
WORKDIR /app

COPY --from=builder /app/bot /app/bot
COPY --from=builder /app/data /app/data

ENV DATA_DIR=/app/data
VOLUME ["/app/data"]

ENTRYPOINT ["/app/bot"]
