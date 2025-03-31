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

.PHONY: build/workflows-service
build/workflows-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=workflows-service' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/workflows-service ./cmd/workflows-service

.PHONY: build/jobs-service
build/jobs-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=jobs-service' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/jobs-service ./cmd/jobs-service

.PHONY: build/notifications-service
build/notifications-service: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=notifications-service' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/notifications-service ./cmd/notifications-service

.PHONY: build/scheduling-worker
build/scheduling-worker: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=workflow-worker' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/scheduling-worker ./cmd/scheduling-worker

.PHONY: build/workflow-worker
build/workflow-worker: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=workflow-worker' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/workflow-worker ./cmd/workflow-worker

.PHONY: build/execution-worker
build/execution-worker: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=execution-worker' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/execution-worker ./cmd/execution-worker

.PHONY: build/joblogs-processor
build/joblogs-processor: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=joblogs-processor' -X 'main.authPrivateKeyPath=certs/auth.ed' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/joblogs-processor ./cmd/joblogs-processor

.PHONY: build/server
build/server: dependencies
	@CGO_ENABLED=0 go build -ldflags "-X 'main.version=v0.0.1' -X 'main.name=server' -X 'main.authPublicKeyPath=certs/auth.ed.pub' -X 'main.authPublicKeyPath=certs/auth.ed.pub'" -o ./.bin/server ./cmd/server

.PHONY: run/users-service
run/users-service: build/users-service
	@./.bin/users-service

.PHONY: run/workflows-service
run/workflows-service: build/workflows-service
	@./.bin/workflows-service

.PHONY: run/jobs-service
run/jobs-service: build/jobs-service
	@./.bin/jobs-service

.PHONY: run/notifications-service
run/notifications-service: build/notifications-service
	@./.bin/notifications-service

.PHONY: run/scheduling-worker
run/scheduling-worker: build/scheduling-worker
	@./.bin/scheduling-worker

.PHONY: run/workflow-worker
run/workflow-worker: build/workflow-worker
	@./.bin/workflow-worker

.PHONY: run/execution-worker
run/execution-worker: build/execution-worker
	@./.bin/execution-worker

.PHONY: run/joblogs-processor
run/joblogs-processor: build/joblogs-processor
	@./.bin/joblogs-processor

.PHONY: run/server
run/server: build/server
	@./.bin/server