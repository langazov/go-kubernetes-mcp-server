.SHELLFLAGS := -eu -c
.DEFAULT_GOAL := build

BINARY   := k8s-mcp-server
CMD_DIR  := ./cmd/k8s-mcp-server
PKG      := ./...
IMAGE    := ghcr.io/langazov/k8s-mcp-server
TAG      := latest

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)

LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Reproducible static build (matches deploy/Dockerfile expectations).
BUILD_FLAGS := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the server binary into ./k8s-mcp-server
	$(BUILD_FLAGS) -o $(BINARY) $(CMD_DIR)

.PHONY: build-all
build-all: ## Compile every package (go build ./...)
	go build $(PKG)

.PHONY: run
run: ## Run the server over stdio (local MCP client mode)
	go run $(CMD_DIR)

.PHONY: vet
vet: ## go vet
	go vet $(PKG)

.PHONY: fmt
fmt: ## Format the code (gofmt -w)
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Verify code is formatted (prints nothing when clean)
	@out=$$(gofmt -l . 2>&1); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

.PHONY: lint
lint: ## Run golangci-lint (config in .golangci.yml)
	golangci-lint run $(PKG)

.PHONY: test
test: ## Run unit tests
	go test $(PKG)

.PHONY: race
race: ## Run tests with the race detector and coverage
	go test -race -cover $(PKG)

.PHONY: cover
cover: ## Generate a coverage report (coverage.out) and print the total
	go test -race -coverprofile=coverage.out $(PKG)
	go tool cover -func=coverage.out | tail -1

.PHONY: tidy
tidy: ## Sync dependencies (go mod tidy)
	go mod tidy

.PHONY: docker
docker: ## Build the container image (requires a built ./k8s-mcp-server)
	docker build -t $(IMAGE):$(TAG) -f deploy/Dockerfile .

.PHONY: check
check: fmt-check build-all vet lint test ## Full pre-merge gate (mirrors AGENTS.md)

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out
