# TelePortal Makefile

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

export CGO_ENABLED ?= 0

# Variables
BINARY_NAME          ?= teleportal
LOADTEST_BINARY_NAME ?= loadtest
BUILD_DIR            ?= build
CMD_PATH             ?= ./cmd/teleportal
LOADTEST_CMD_PATH    ?= ./cmd/loadtest
ENV_FILE             ?= .env

# Go toolchain
GO   ?= go
PKGS ?= ./...
CI   ?=

.PHONY: all help install-tools build build-loadtest build-all test run qa fix fmt vet lint nilaway arch-go loc fieldalignment vuln clean

all: help

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t24

install-tools: ## Download deps and build the pinned go tools into the module cache
	$(GO) mod download
	$(GO) tool golangci-lint version
	$(GO) tool gofumpt --version
	$(GO) tool arch-go --version
	$(GO) tool govulncheck -version >/dev/null 2>&1 || true
	$(GO) tool nilaway -V=full >/dev/null 2>&1 || true
	$(GO) tool fieldalignment -V=full >/dev/null 2>&1 || true

build-all: build build-loadtest ## Build both the core service and the loadtest tool

build: ## Build the core service binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

build-loadtest: ## Build the standalone load test tool
	@echo "Building $(LOADTEST_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(LOADTEST_BINARY_NAME) $(LOADTEST_CMD_PATH)

test: ## Run all tests using the standard library testing package
	@echo "Running tests..."
	$(GO) test -v $(PKGS)

run: build ## Build and run the service, loading variables from .env
	@stty -echoctl 2>/dev/null || true
	@trap 'stty echoctl 2>/dev/null || true; exit 0' INT; \
	if [ -f $(ENV_FILE) ]; then \
		echo "Loading $(ENV_FILE) and starting $(BINARY_NAME)..."; \
		export $$(grep -v '^#' $(ENV_FILE) | xargs) && ./$(BUILD_DIR)/$(BINARY_NAME); \
	else \
		echo "$(ENV_FILE) not found, starting $(BINARY_NAME) with defaults..."; \
		./$(BUILD_DIR)/$(BINARY_NAME); \
	fi
	@stty echoctl 2>/dev/null || true

qa: fix fmt vet lint nilaway arch-go loc fieldalignment ## Full static-analysis gate: fix -> fmt -> vet -> lint -> nilaway -> arch-go -> loc -> fieldalignment
	@echo "QA checks completed successfully."

fix: ## go fix modernizers: rewrite locally, fail in CI
ifeq ($(CI),)
	$(GO) fix $(PKGS)
else
	$(GO) fix -diff $(PKGS)
endif

fmt: ## gofumpt: rewrite locally, fail in CI
ifeq ($(CI),)
	$(GO) tool gofumpt -w .
else
	@out=$$($(GO) tool gofumpt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi
endif

vet: ## go vet
	$(GO) vet $(PKGS)

lint: ## golangci-lint
	$(GO) tool golangci-lint run $(PKGS)

nilaway: ## NilAway nil-panic analysis
	$(GO) tool nilaway -exclude-test-files $(PKGS)

arch-go: ## architecture rules from arch-go.yml
	$(GO) tool arch-go

fieldalignment: ## struct field ordering
	$(GO) tool fieldalignment $(PKGS)

loc: ## Fail on any Go file over 1500 lines of code, comments and blanks excluded (MAXLOC= override)
	scripts/loc.sh -m $(or $(MAXLOC),1500)

vuln: ## govulncheck against the Go vuln DB (needs network; not part of qa)
	$(GO) tool govulncheck $(PKGS)

clean: ## Remove build artifacts
	@echo "Cleaning build directory..."
	@rm -rf $(BUILD_DIR)
