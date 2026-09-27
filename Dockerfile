# Runtime base: pure-Go static binaries, no libc, no shell. Runs as uid 65532.
# Pinned by digest; bump via Renovate/Dependabot. Declared before the first FROM so runtime stage can use it.
ARG RUNTIME=gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

# Builder: Debian/glibc-based so it matches the distroless runtime. Never use golang:alpine here.
FROM golang:1.27-trixie AS build
WORKDIR /src

# Copy go.mod and go.sum files
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the source code
COPY cmd/teleportal ./cmd/teleportal
COPY internal ./internal
COPY pkg ./pkg

# Create log and data directories owned by nonroot (uid 65532)
RUN mkdir -p /out/var/log/teleportal /out/var/lib/teleportal/recordings && \
    chown -R 65532:65532 /out/var/log/teleportal /out/var/lib/teleportal/recordings

# Build the application with caching
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/teleportal ./cmd/teleportal

# Production image. Last stage, so it is also the default build target.
FROM ${RUNTIME} AS runtime

# Set timezone, default to UTC
ARG TZ=UTC
ENV TZ=${TZ}

# Copy directories with nonroot ownership
COPY --from=build --chown=65532:65532 /out/var/log/teleportal /var/log/teleportal
COPY --from=build --chown=65532:65532 /out/var/lib/teleportal/recordings /var/lib/teleportal/recordings

# Copy the binary from the build stage
COPY --from=build /out/teleportal /teleportal

# Expose ports (SIP, HTTP, RTP, Web)
# Note: RTP range is large and handled via environment variables, 
# but we expose the standard signaling and control ports here.
EXPOSE 5060/udp 5060/tcp 8080/tcp 3000/tcp

# Run the application
ENTRYPOINT ["/teleportal"]
