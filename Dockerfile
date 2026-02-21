FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /pr-fetcher ./cmd/pr-fetcher
RUN CGO_ENABLED=0 go build -o /pr-evaluator ./cmd/pr-evaluator

# --- pr-fetcher ---
FROM alpine:3.21 AS pr-fetcher
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /pr-fetcher /usr/local/bin/pr-fetcher
VOLUME /data
VOLUME /config
ENTRYPOINT ["/usr/local/bin/pr-fetcher"]

# --- pr-evaluator ---
FROM alpine:3.21 AS pr-evaluator
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /pr-evaluator /usr/local/bin/pr-evaluator
VOLUME /data
ENTRYPOINT ["/usr/local/bin/pr-evaluator"]
