# ── build stage ──────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -trimpath \
      -o /relay ./cmd/relay

# ── runtime stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12

COPY --from=build /relay /relay

VOLUME  ["/data"]
EXPOSE  8080

ENV RELAY_DATA=/data \
    RELAY_PORT=8080

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD ["/relay", "healthcheck"]

ENTRYPOINT ["/relay"]
