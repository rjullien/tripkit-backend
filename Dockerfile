# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0: excludes sqlite (cgo build tag), only postgres driver compiled.
# No C compiler needed → smaller, faster build.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o tripkit-api ./cmd/api

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=build /app/tripkit-api /usr/local/bin/tripkit-api

EXPOSE 3001

CMD ["tripkit-api"]
