# SkinNexus multi-stage image: web build -> go build -> minimal runtime.
#   docker build -t skin-nexus .
# Data (textures/keys/themes) lives in the /data volume — upgrades never
# touch it. Schema bootstrap happens at boot when SKIN_AUTO_MIGRATE=true.

# ---- stage 1: web ----
FROM node:22-alpine AS web
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
# style.css @imports the design-token source of truth from the server tree
# (../../server/cmd/skin-nexus/theme-base.css) — mirror the repo layout.
COPY server/cmd/skin-nexus/theme-base.css /build/server/cmd/skin-nexus/theme-base.css
COPY web/ ./
RUN npm run build

# ---- stage 2: server ----
# The golang image sets GOTOOLCHAIN=local, so the builder is the toolchain that
# compiles us — it must be at least the `go` directive in go.mod, and moving it
# forward is how a toolchain patch reaches the image. The exact version is not a
# guess: the admin status page reports the running binary's runtime version.
FROM golang:1.27-alpine AS server
# goproxy.cn is fast from mainland China but slow/unreliable from GitHub's
# US runners; CI overrides this build arg with proxy.golang.org.
ARG GOPROXY=https://goproxy.cn,direct
ARG BUILD_VERSION=0.6.0
ARG BUILD_COMMIT=
ARG BUILD_TIME=
ARG BUILD_BRANCH=
ENV GOPROXY=${GOPROXY}
WORKDIR /build/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w \
        -X main.version=${BUILD_VERSION} \
        -X main.commit=${BUILD_COMMIT} \
        -X main.buildTime=${BUILD_TIME} \
        -X main.buildBranch=${BUILD_BRANCH}" \
      -o /out/skin-nexus ./cmd/skin-nexus

# ---- stage 3: runtime ----
# The shipped layer is pinned to a supported Alpine branch: 3.20 lost its main
# repository support on 2026-04-01, which quietly turned every image built since
# then into an unpatched base. Bump this when the branch below reaches its own
# end-of-support date (see docs/deploy.md §base 镜像更新节奏); the build stages
# stay on floating tags on purpose — `--pull` in CI refetches them, so a rebuild
# is the patch, and the running binary reports the exact Go version it was built
# with on the admin status page.
FROM alpine:3.24
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
