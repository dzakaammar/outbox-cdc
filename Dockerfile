FROM golang:1.22-alpine AS builder

LABEL maintainer="Dzaka Ammar Ibrahim"
RUN apk add --update --no-cache curl ca-certificates git
WORKDIR /app
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main -a -installsuffix cgo

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/main .
COPY --from=builder /usr/local/go/lib/time/zoneinfo.zip /
ENV ZONEINFO=/zoneinfo.zip

EXPOSE 8080
ENTRYPOINT [ "/app/main" ]