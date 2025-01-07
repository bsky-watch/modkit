FROM golang:1 AS builder
ARG CMD
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY cmd/$CMD ./cmd/$CMD
RUN go build -trimpath -o /app/main ./cmd/$CMD

FROM alpine:latest AS certs
RUN apk --update add ca-certificates

FROM debian:stable-slim
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /app/main .
ENTRYPOINT ["./main"]
