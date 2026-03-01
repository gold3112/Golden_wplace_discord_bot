# syntax=docker/dockerfile:1

FROM golang:1.21 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o bot ./cmd/bot

FROM gcr.io/distroless/base-debian12
WORKDIR /app

COPY --from=builder /app/bot /app/bot
RUN mkdir -p /app/data

ENV DATA_DIR=/app/data
VOLUME ["/app/data"]

ENTRYPOINT ["/app/bot"]
