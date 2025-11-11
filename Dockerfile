# ---
# Build stage
# ---
FROM golang:1.25-alpine3.22 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN \
    CGO_ENABLED=0 GOOS=linux \
        go build -a -installsuffix cgo -o /usr/local/npm_replicator

# ---
# Final stage
# ---
FROM alpine:3.22
RUN apk --no-cache add ca-certificates

COPY --from=builder /usr/local/npm_replicator /usr/local/npm_replicator

EXPOSE 50111/tcp

ENTRYPOINT ["/usr/local/npm_replicator"]

