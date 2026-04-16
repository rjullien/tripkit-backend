# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS build

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o tripkit-api ./cmd/api

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=build /app/tripkit-api /usr/local/bin/tripkit-api

RUN mkdir -p /data
ENV DB_PATH=/data/tripkit.db

EXPOSE 3001

ENTRYPOINT ["tripkit-api"]
