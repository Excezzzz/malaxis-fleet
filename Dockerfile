FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata postgresql-client docker-cli
WORKDIR /app
COPY master_server .
RUN chmod +x master_server
EXPOSE 8000
CMD ["./master_server"]
