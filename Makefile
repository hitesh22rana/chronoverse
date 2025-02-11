GO_BIN?=$(shell pwd)/.bin
SHELL:=env PATH=$(GO_BIN):$(PATH) $(SHELL)

.PHONY: generate
generate:
	@buf --version > /dev/null 2>&1 || (echo "buf is not installed. Please install buf by referring to https://docs.buf.build/installation" && exit 1)
	@rm -rf pkg/proto && rm -rf pkg/openapiv2 && buf dep update && buf generate

.PHONY: dependencies
dependencies: generate
	@go mod tidy -v

.PHONY: lint
lint: dependencies
	@golangci-lint run

.PHONY: lint-fix
lint-fix: dependencies
	@golangci-lint run --fix

PHONY: test
test: dependencies
	@go test -v ./...

.PHONY: tools
tools:
	@mkdir -p ${GO_BIN}
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ${GO_BIN} v1.63.4
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % sh -c 'GOBIN=${GO_BIN} go install %'

.PHONY: build/auth-service
build/auth-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=auth-service'" -o ./.bin/auth-service ./cmd/auth-service

.PHONY: run/auth-service
run/auth-service: build/auth-service
	@./.bin/auth-service