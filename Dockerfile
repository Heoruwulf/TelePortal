# Build stage
FROM golang:1.26-alpine AS builder

# Set the working directory
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies with caching
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the source code
COPY cmd/teleportal ./cmd/teleportal/
COPY internal/ ./internal/
COPY pkg/ ./pkg/

# Build the application with caching
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /app/teleportal ./cmd/teleportal/main.go

# Run stage
FROM alpine:3.22.4

# Set timezone, default to UTC
ARG TZ=UTC
ENV TZ=${TZ}

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create a non-root user
RUN adduser -D -u 1000 teleportal

# Create log and data directories and set permissions
RUN mkdir -p /var/log/teleportal /var/lib/teleportal/recordings && \
    chown -R teleportal:teleportal /var/log/teleportal /var/lib/teleportal/recordings

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/teleportal /app/teleportal

# Set the user to run the application
USER teleportal

# Expose ports (SIP, HTTP, RTP, Web)
# Note: RTP range is large and handled via environment variables, 
# but we expose the standard signaling and control ports here.
EXPOSE 5060/udp 5060/tcp 8080/tcp 3000/tcp

# Run the application
ENTRYPOINT ["/app/teleportal"]
