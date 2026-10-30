# SkinNexus multi-stage image: web build -> go build -> minimal runtime.
#   docker build -t skin-nexus .
# Data (textures/keys/themes) lives in the /data volume — upgrades never
# touch it. Schema bootstrap happens at boot when SKIN_AUTO_MIGRATE=true.

# ---- stage 1: web ----
FROM node:22-alpine AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- stage 2: server ----
# go.mod requires go >= 1.26; the golang image sets GOTOOLCHAIN=local so the
# builder tag must match the go directive (no auto toolchain upgrade inside).
FROM golang:1.26-alpine AS server
# goproxy.cn is fast from mainland China but slow/unreliable from GitHub's
# US runners; CI overrides this build arg with proxy.golang.org.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /build/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/skin-nexus ./cmd/skin-nexus

# ---- stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=server /out/skin-nexus /app/skin-nexus
COPY --from=web /build/web/dist /app/web/dist
COPY server/migrations /app/migrations

ENV SKIN_LISTEN=:8080 \
    SKIN_DATA_DIR=/data \
    SKIN_MIGRATIONS_DIR=/app/migrations \
    SKIN_AUTO_MIGRATE=true

VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/skin-nexus"]
