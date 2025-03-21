GO_BIN?=$(shell pwd)/.bin
SHELL:=env PATH=$(GO_BIN):$(PATH) $(SHELL)

.PHONY: generate
generate:
	@buf --version > /dev/null 2>&1 || (echo "buf is not installed. Please install buf by referring to https://docs.buf.build/installation" && exit 1)
	@rm -rf pkg/proto && buf dep update && buf generate

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
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ${GO_BIN} v1.64.5
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % sh -c 'GOBIN=${GO_BIN} go install %@latest'

.PHONY: build/users-service
build/users-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=users-service' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/users-service ./cmd/users-service

.PHONY: build/jobs-service
build/jobs-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=jobs-service' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/jobs-service ./cmd/jobs-service

.PHONY: build/scheduling-job
build/scheduling-job: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=scheduling-job'" -o ./.bin/scheduling-job ./cmd/scheduling-job

.PHONY: build/workflow-job
build/workflow-job: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=workflow-job' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/workflow-job ./cmd/workflow-job

.PHONY: build/execution-job
build/execution-job: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=execution-job' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/execution-job ./cmd/execution-job

.PHONY: build/server
build/server: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=server' -X 'main.authPublicKeyPath=certs/auth.ed.pub' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/server ./cmd/server

.PHONY: run/users-service
run/users-service: build/users-service
	@./.bin/users-service

.PHONY: run/jobs-service
run/jobs-service: build/jobs-service
	@./.bin/jobs-service

.PHONY: run/scheduling-job
run/scheduling-job: build/scheduling-job
	@./.bin/scheduling-job

.PHONY: run/workflow-job
run/workflow-job: build/workflow-job
	@./.bin/workflow-job

.PHONY: run/execution-job
run/execution-job: build/execution-job
	@./.bin/execution-job

.PHONY: run/server
run/server: build/server
	@./.bin/server