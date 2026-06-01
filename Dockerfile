FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN cp packaging/logmcp.service cmd/assets/logmcp.service && \
    CGO_ENABLED=0 go build -o logmcp .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/logmcp /usr/local/bin/logmcp
COPY docker/config.yaml /etc/logmcp/config.yaml
EXPOSE 7788
CMD ["logmcp", "serve"]
