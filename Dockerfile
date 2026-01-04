# syntax=docker/dockerfile:1

# Build arguments for version control
# renovate: datasource=docker depName=golang
ARG GO_VERSION=1.25
# Must match DEFAULT_HAPROXY in versions.env
# renovate: datasource=docker depName=haproxytech/haproxy-debian versioning=semver
ARG HAPROXY_VERSION=3.2
ARG GIT_COMMIT=unknown
ARG GIT_TAG=unknown
ARG SOURCE_HASH=unknown

# -----------------------------------------------------------------------------
# Builder stage - compile the Go binary
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Leverage Docker cache for Go modules
# Copy go.mod and go.sum first to cache module downloads
# Note: We intentionally avoid --mount=type=cache here so that downloaded
# modules become part of the layer and can be cached in CI registry caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy only source directories needed for compilation
# (explicit copies avoid cache invalidation from README, docs, tests, etc.)
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/

# Build arguments for cross-compilation and version info
ARG TARGETOS
ARG TARGETARCH
ARG TARGETPLATFORM
ARG GIT_COMMIT
ARG GIT_TAG
ARG SOURCE_HASH

# Build the controller binary
# - CGO_ENABLED=0: static binary, no C dependencies
# - GOOS/GOARCH: cross-compilation for target platform
# - -trimpath: remove file system paths from binary
# - -buildvcs=false: reproducible builds (no VCS info embedded)
# - -pgo=auto: enable profile-guided optimization if default.pgo exists
# - -ldflags: linker flags for optimization and version info
#   - -s: strip debug information
#   - -w: strip DWARF debug information
#   - -X: inject version variables
# Output to platform-structured path for compatibility with goreleaser dockers_v2
RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -buildvcs=false \
    -pgo=auto \
    -ldflags="-s -w -X main.version=${GIT_TAG} -X main.commit=${GIT_COMMIT} -X main.sourceHash=${SOURCE_HASH}" \
    -o /build/${TARGETPLATFORM}/haptic-controller \
    ./cmd/controller

# -----------------------------------------------------------------------------
# Binary output stage - exports the controller binary
# This stage can be overridden via --build-context binary=<path> to use a
# pre-compiled binary instead of building from source (used in GitLab CI).
# The platform-structured path (e.g., linux/amd64/haptic-controller) ensures
# compatibility with both CI builds and goreleaser dockers_v2.
# -----------------------------------------------------------------------------
FROM scratch AS binary
ARG TARGETPLATFORM
COPY --from=builder /build/${TARGETPLATFORM}/haptic-controller /${TARGETPLATFORM}/haptic-controller

# -----------------------------------------------------------------------------
# Runtime stage - minimal image with HAProxy for validation
# -----------------------------------------------------------------------------
FROM haproxytech/haproxy-debian:${HAPROXY_VERSION} AS runtime

# TARGETPLATFORM is set automatically by buildx (e.g., linux/amd64)
ARG TARGETPLATFORM

# Copy the controller binary from the 'binary' stage
# When using --build-context binary=<path>, this copies from the external context
COPY --from=binary /${TARGETPLATFORM}/haptic-controller /usr/local/bin/haptic-controller

# Ensure binary is executable
RUN chmod +x /usr/local/bin/haptic-controller

# Create validation directories for HAProxy configuration validation
# These directories must be writable by the haproxy user
RUN mkdir -p /usr/local/etc/haproxy/maps \
             /usr/local/etc/haproxy/certs \
             /usr/local/etc/haproxy/general && \
    chown -R haproxy:haproxy /usr/local/etc/haproxy

# Switch to haproxy user for security
# The haproxy user is pre-created by the haproxytech base image
USER haproxy

# Override STOPSIGNAL from base image (SIGUSR1) to SIGTERM
# The haproxy base image uses SIGUSR1 for HAProxy's graceful shutdown,
# but our Go controller expects SIGTERM for graceful shutdown
STOPSIGNAL SIGTERM

# Set the entrypoint to the controller
ENTRYPOINT ["/usr/local/bin/haptic-controller"]

# Default command (can be overridden)
CMD ["run"]
