# Base image
FROM golang:1.26.5 AS build

# Build arguments
ARG VERSION
ARG NAME
ARG PRIVATE_KEY_PATH
ARG PUBLIC_KEY_PATH
ARG PKG_PATH="github.com/hitesh22rana/chronoverse/internal/pkg/svc"

WORKDIR /app

# Copy the Go module files
COPY go.mod .
COPY go.sum .

# Download the Go module dependencies
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy only the specific service subdirectory and other necessary files
COPY ./cmd/${NAME} ./cmd/${NAME}
COPY ./internal ./internal
COPY ./pkg ./pkg

# Build the service with ldflags
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -trimpath \
    -ldflags "-s -w \
    -X '${PKG_PATH}.version=${VERSION}' \
    -X '${PKG_PATH}.name=${NAME}' \
    -X '${PKG_PATH}.authPrivateKeyPath=${PRIVATE_KEY_PATH}' \
    -X '${PKG_PATH}.authPublicKeyPath=${PUBLIC_KEY_PATH}'" \
    -o /go/bin/service ./cmd/${NAME}

# Final minimal stage
FROM alpine:3.24.1

# Keep this identity stable: Kubernetes fsGroup and Compose Docker-proxy role
# directories grant private-key access only to app UID/GID 100:101.
RUN addgroup -S -g 101 app && adduser -S -D -H -u 100 -G app app

# Set the build arguments
ARG NAME

# Create directories with proper permissions
RUN mkdir -p /certs && \
    chown -R app:app /certs && \
    chmod -R 550 /certs

# Copy binary and set permissions
COPY --from=build --chown=app:app /go/bin/service /bin/service
RUN chmod 500 /bin/service

# Install necessary runtime dependencies and grpc-health-probe
RUN apk --no-cache add ca-certificates tzdata wget && \
    GRPC_HEALTH_PROBE_VERSION=v0.4.53 && \
    ARCH=$(uname -m) && \
    case ${ARCH} in \
    x86_64) ARCH="amd64" ;; \
    aarch64) ARCH="arm64" ;; \
    *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac && \
    wget -qO/bin/grpc-health-probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-${ARCH} && \
    chmod +x /bin/grpc-health-probe && \
    apk del wget

# Switch to non-root user
USER app

# Add security labels
LABEL org.opencontainers.image.source="https://github.com/hitesh22rana/chronoverse"
LABEL org.opencontainers.image.description="Chronoverse ${NAME}"
LABEL org.opencontainers.image.licenses="MIT"

# Run service
CMD ["/bin/service"]
