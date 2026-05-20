BINARY_NAME := capydb
MAIN_PACKAGE := ./cmd/capydb
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
CGO_ENABLED ?= 0

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(GIT_COMMIT) \
	-X main.date=$(BUILD_TIME) \
	-X main.builtBy=make

DIST_DIR := dist
BUILD_DIR := build

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "Targets:"
	@echo "  build             Build the CLI"
	@echo "  build-all         Cross-compile release binaries"
	@echo "  fmt               Format Go code"
	@echo "  lint              Run golangci-lint or go vet"
	@echo "  test              Run unit tests with race detection"
	@echo "  check             Run fmt, lint, and test"
	@echo "  docker-build      Build the local Docker image"
	@echo "  docker-run        Run the local Docker image"
	@echo "  release-check     Validate the GoReleaser config"
	@echo "  release-snapshot  Build a local snapshot release"
	@echo "  clean             Remove build artifacts"

.PHONY: build
build:
	CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) $(MAIN_PACKAGE)

.PHONY: build-all
build-all: clean
	@mkdir -p $(DIST_DIR)
	@for pair in "darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64" "windows/amd64" "windows/arm64"; do \
		os=$${pair%%/*}; arch=$${pair##*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "Building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=$(CGO_ENABLED) \
			go build -trimpath -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-$$os-$$arch$$ext $(MAIN_PACKAGE); \
	done

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
	else \
		go vet ./...; \
	fi

.PHONY: test
test:
	go test -v -race ./...

.PHONY: check
check: fmt lint test

.PHONY: docker-build
docker-build:
	docker build \
		--build-arg BUILD_VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$(BUILD_TIME) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(BINARY_NAME):$(VERSION) \
		-t $(BINARY_NAME):latest \
		.

.PHONY: docker-run
docker-run:
	docker run --rm $(BINARY_NAME):latest --help

.PHONY: release-check
release-check:
	RELEASE_REPO_OWNER=$${RELEASE_REPO_OWNER:-local} \
	RELEASE_REPO_NAME=$${RELEASE_REPO_NAME:-capydb-cli} \
	IMAGE_REPOSITORY=$${IMAGE_REPOSITORY:-ghcr.io/local/capydb-cli} \
	REPOSITORY_URL=$${REPOSITORY_URL:-https://github.com/local/capydb-cli} \
	goreleaser check

.PHONY: release-snapshot
release-snapshot:
	RELEASE_REPO_OWNER=$${RELEASE_REPO_OWNER:-local} \
	RELEASE_REPO_NAME=$${RELEASE_REPO_NAME:-capydb-cli} \
	IMAGE_REPOSITORY=$${IMAGE_REPOSITORY:-ghcr.io/local/capydb-cli} \
	REPOSITORY_URL=$${REPOSITORY_URL:-https://github.com/local/capydb-cli} \
	goreleaser release --snapshot --clean --skip=publish

.PHONY: clean
clean:
	go clean
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*
	rm -rf $(DIST_DIR) $(BUILD_DIR)
	rm -f coverage.out coverage.html
