# Base image
FROM golang:1.24.3 AS build

# Build arguments
ARG VERSION
ARG NAME
ARG PRIVATE_KEY_PATH
ARG PUBLIC_KEY_PATH
ARG PKG_PATH="github.com/hitesh22rana/chronoverse/internal/pkg/svc"

# Set working directory
WORKDIR /app

# Install protoc and required packages
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    BIN="/usr/local/bin" && \
    VER="1.50.0" && \
    curl -sSL \
    "https://github.com/bufbuild/buf/releases/download/v${VER}/buf-$(uname -s)-$(uname -m)" \
    -o "${BIN}/buf" && \
    chmod +x "${BIN}/buf" && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install protoc-gen-go and protoc-gen-go-grpc
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Add the Go binaries to the PATH
RUN export GOROOT=/usr/local/go
RUN export GOPATH=$HOME/go
RUN export GOBIN=$GOPATH/bin
RUN export PATH=$PATH:$GOROOT:$GOPATH:$GOBIN

# Copy the Go module files
COPY go.mod .
COPY go.sum .

# Download the Go module dependencies
RUN go mod download

# Copy the source code
COPY . .

# Compile the protocol buffer files and generate the Go files
RUN rm -rf pkg/proto && buf dep update && buf generate

# Build the service with ldflags
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -trimpath \
    -ldflags "-s -w \
    -X '${PKG_PATH}.version=${VERSION}' \
    -X '${PKG_PATH}.name=${NAME}' \
    -X '${PKG_PATH}.authPrivateKeyPath=${PRIVATE_KEY_PATH}' \
    -X '${PKG_PATH}.authPublicKeyPath=${PUBLIC_KEY_PATH}'" \
    -o /go/bin/service cmd/${NAME}/main.go

# Final minimal stage
FROM alpine:latest

# Create a non-root user and group
RUN addgroup -S app && adduser -S -G app app

# Set the build arguments
ARG NAME

# Create directories with proper permissions
RUN mkdir -p /certs && \
    chown -R app:app /certs && \
    chmod -R 550 /certs

# Copy certificates and set permissions
COPY --from=build --chown=app:app /app/certs /certs

# Copy binary and set permissions
COPY --from=build --chown=app:app /go/bin/service /bin/service
RUN chmod 500 /bin/service

# Install necessary runtime dependencies and grpc-health-probe
RUN apk --no-cache add ca-certificates tzdata wget && \
    GRPC_HEALTH_PROBE_VERSION=v0.4.39 && \
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