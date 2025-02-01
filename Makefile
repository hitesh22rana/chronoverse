SHELL := /bin/bash -o pipefail

.PHONY: generate
generate:
	@rm -rf ./pkg/proto/ && mkdir -p ./pkg/proto && for file in proto/*.proto; do \
		base=$$(basename $$file); \
		name=$${base%.*}; \
		mkdir -p ./pkg/proto/$$name; \
		protoc --go_out=paths=source_relative:./pkg/proto/$$name --go-grpc_out=paths=source_relative:./pkg/proto/$$name \
		--proto_path=proto $$file; \
	done

.PHONY: dependencies
dependencies: generate
	@go mod tidy

.PHONY: lint
lint: dependencies
	@golangci-lint run

PHONY: test
test: dependencies
	@go test ./...

.PHONY: build/auth-service
build/auth-service: dependencies
	@CGO_ENABLED=0 go build -o ./bin/auth-service ./cmd/auth-service 

.PHONY: run/auth-service
run/auth-service: build/auth-service
	@./bin/auth-service