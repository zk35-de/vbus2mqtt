# syntax=docker/dockerfile:1

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Cache deps separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -trimpath \
    -o /bin/vbus2mqtt ./cmd/vbus2mqtt

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

LABEL org.opencontainers.image.title="vbus2mqtt" \
      org.opencontainers.image.description="RESOL VBus (USB serial) → MQTT bridge with web UI" \
      org.opencontainers.image.source="https://github.com/zk35-de/vbus2mqtt"

# dialout group (GID 20 on Alpine) for serial port access.
RUN addgroup -g 20 dialout 2>/dev/null || true && \
    adduser -u 1000 -G dialout -s /sbin/nologin -D vbus

# Persistent config storage; owner vbus so the process can write.
RUN mkdir -p /data && chown vbus:dialout /data

USER vbus

COPY --from=builder /bin/vbus2mqtt /bin/vbus2mqtt

EXPOSE 8080

VOLUME ["/data"]

ENTRYPOINT ["/bin/vbus2mqtt"]
