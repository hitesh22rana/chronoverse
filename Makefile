GO_BIN?=$(shell pwd)/.bin
PROTOC_GEN_GO_VERSION?=$(shell go list -m -f '{{.Version}}' google.golang.org/protobuf)
PROTOC_GEN_GO_GRPC_VERSION?=v1.6.2
export PATH := $(GO_BIN):$(PATH)
PKG_PATH=github.com/hitesh22rana/chronoverse/internal/pkg/svc
APP_VERSION?=v0.0.1 # Default version

.PHONY: generate
generate:
	@buf --version > /dev/null 2>&1 || (echo "buf is not installed. Please install buf by referring to https://docs.buf.build/installation" && exit 1)
	@rm -rf pkg/proto && buf dep update && buf generate

.PHONY: dependencies
dependencies: generate
	@go mod tidy -v

.PHONY: lint
lint: dependencies
	@${GO_BIN}/golangci-lint run

.PHONY: lint/fix
lint/fix: dependencies
	@${GO_BIN}/golangci-lint run --fix

# Runs every unit suite with the race detector. Docker-backed integration
# tests self-skip under -short and are covered by test/integration.
.PHONY: test/short
test/short: dependencies
	@go test -race -short ./...

# Runs all Docker-backed suites (Testcontainers and direct-daemon tests) by
# selecting every TestIntegration* test. Requires a running Docker daemon.
# The explicit -timeout leaves headroom over Go's default 10m per-package
# limit: the joblogs suite boots five containers plus sequential Eventually
# windows.
.PHONY: test/integration
test/integration: dependencies
	@go test -race -v -count=1 -timeout=20m -run 'TestIntegration' ./...

.PHONY: k8s/setup
k8s/setup:
	@./scripts/k8s/setup.sh

.PHONY: compose/validate
compose/validate:
	@./scripts/compose/validate.sh

.PHONY: k8s/render/local
k8s/render/local:
	@kubectl kustomize infra/k8s/overlays/local

.PHONY: k8s/render/production
k8s/render/production:
	@kubectl kustomize infra/k8s/overlays/production

.PHONY: k8s/dry-run/local
k8s/dry-run/local:
	@kubectl apply --dry-run=client --validate=false -k infra/k8s/overlays/local

.PHONY: k8s/dry-run/production
k8s/dry-run/production:
	@kubectl apply --dry-run=client --validate=false -k infra/k8s/overlays/production

.PHONY: tools
tools:
	@mkdir -p ${GO_BIN}
	@GOBIN=${GO_BIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
	@grep _ tools.go | awk -F'"' '{print $$2}' | while read tool; do \
		version=latest; \
		if [ "$$tool" = "google.golang.org/protobuf/cmd/protoc-gen-go" ]; then version=${PROTOC_GEN_GO_VERSION}; fi; \
		if [ "$$tool" = "google.golang.org/grpc/cmd/protoc-gen-go-grpc" ]; then version=${PROTOC_GEN_GO_GRPC_VERSION}; fi; \
		GOBIN=${GO_BIN} go install "$$tool@$$version"; \
	done

.PHONY: mockgen
mockgen: tools
	@go generate -v ./...

SERVICES := users-service workflows-service jobs-service notifications-service analytics-service scheduling-worker workflow-worker execution-worker runtime-agent joblogs-processor analytics-processor outbox-relay database-migration server
BUILD_TARGETS := $(addprefix build/,$(SERVICES))
RUN_TARGETS := $(addprefix run/,$(SERVICES))

.PHONY: $(BUILD_TARGETS) $(RUN_TARGETS) build/all

ISSUER = $*
build/database-migration: ISSUER = server

$(BUILD_TARGETS): build/%: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X '${PKG_PATH}.version=${APP_VERSION}' -X '${PKG_PATH}.name=$*' -X '${PKG_PATH}.authPrivateKeyPath=certs/issuers/$(ISSUER)/auth.ed' -X '${PKG_PATH}.authPublicKeyPath=certs/issuers/$(ISSUER)/auth.ed.pub'" -o ./.bin/$* ./cmd/$*

build/all: $(BUILD_TARGETS)
	@echo "All services and workers built successfully."

$(RUN_TARGETS): run/%: build/%
	@./.bin/$*
