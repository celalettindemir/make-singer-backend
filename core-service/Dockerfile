# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/server

# Final stage
FROM alpine:3.19

WORKDIR /app

# Install ca-certificates for HTTPS and curl for healthcheck
RUN apk --no-cache add ca-certificates curl

# Copy binary from builder
COPY --from=builder /app/main .
COPY --from=builder /app/config.yaml .

# Expose port
EXPOSE 8000

# Run
CMD ["./main"]
