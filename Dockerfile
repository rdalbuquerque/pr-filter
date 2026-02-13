FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /pr-fetcher ./cmd/pr-fetcher

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /pr-fetcher /usr/local/bin/pr-fetcher
VOLUME /data
VOLUME /config
ENTRYPOINT ["/usr/local/bin/pr-fetcher"]
