# Stage 1: build the Vue dashboard
FROM node:24-alpine AS web-builder
# Allow overriding the npm registry (some networks block/slow npmjs.org):
#   docker build --build-arg NPM_REGISTRY=https://registry.npmmirror.com .
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /app/web
# The committed internal/api/web/dist (regenerated on every deploy) is copied
# in as a guaranteed fallback: if npm ci / npm run build fail here (registry
# blocked, network flake, cache state), the last known-good dashboard is still
# embedded instead of aborting the build with
# "pattern web/dist: no matching files found".
COPY internal/api/web/package*.json ./
RUN npm ci --registry=$NPM_REGISTRY || echo "[!] npm ci failed - falling back to committed dist"
# The root VERSION file is the single source of truth for the version string;
# vue.config.js reads it via ../../../VERSION, so it must exist at /VERSION.
COPY VERSION /VERSION
COPY internal/api/web/ ./
RUN npm run build || echo "[!] npm run build failed - falling back to committed dist"

# Stage 2: build the Go backend (embeds web/dist and deploy assets)
FROM golang:1.26-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/dist internal/api/web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o master_server ./cmd/server/main.go

# Stage 3: minimal runtime
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata postgresql-client docker-cli
WORKDIR /app
COPY --from=go-builder /app/master_server .
# VERSION is read at runtime by the Go backend (internal/config loadAppVersion).
COPY VERSION /app/VERSION
RUN chmod +x master_server
EXPOSE 8000
CMD ["./master_server"]
