# Stage 1: build the Vue dashboard
FROM node:24-alpine AS web-builder
WORKDIR /app/web
COPY internal/api/web/package*.json ./
RUN npm ci
COPY internal/api/web/ ./
RUN npm run build

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
RUN chmod +x master_server
EXPOSE 8000
CMD ["./master_server"]
