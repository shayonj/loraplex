.PHONY: build test lint clean e2e docker loadgen mock-vllm

BINARY := loraplex
BUILD_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
GOFLAGS := -trimpath -ldflags="$(LDFLAGS)"

build:
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/loraplex

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/loraplex

loadgen:
	go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/loadgen ./cmd/loadgen

mock-vllm:
	go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/mock-vllm ./cmd/mock-vllm

test:
	go test -race -count=1 ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

e2e:
	go test -race -count=1 -tags=e2e -timeout=120s ./e2e/

lint:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

docker:
	docker build -t loraplex:latest .
