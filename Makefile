# TelePortal Makefile

# Variables
BINARY_NAME=teleportal
LOADTEST_BINARY_NAME=loadtest
BUILD_DIR=build
CMD_PATH=./cmd/teleportal/main.go
LOADTEST_CMD_PATH=./cmd/loadtest/main.go
ENV_FILE=.env

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOFMT=$(GOCMD) fmt
STATICCHECK=staticcheck
FIELDALIGNMENT=fieldalignment

.PHONY: all build build-loadtest test run qa clean help fmt vet fieldalignment staticcheck

all: help

build: ## Build the core service binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

build-loadtest: ## Build the standalone load test tool
	@echo "Building $(LOADTEST_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(LOADTEST_BINARY_NAME) ./cmd/loadtest

test: ## Run all tests using the standard library testing package
	@echo "Running tests..."
	$(GOTEST) -v ./...

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

qa: fmt vet fieldalignment staticcheck ## Run quality assurance tools (fmt, vet, fieldalignment, staticcheck)
	@echo "QA checks completed."

fmt: ## Run go fmt on all packages
	@echo "Running go fmt..."
	$(GOFMT) ./...

vet: ## Run go vet on all packages
	@echo "Running go vet..."
	$(GOVET) ./...

fieldalignment: ## Run fieldalignment to check struct layouts
	@echo "Running fieldalignment..."
	@which $(FIELDALIGNMENT) > /dev/null || (echo "fieldalignment not found. Install with: go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest" && exit 1)
	$(FIELDALIGNMENT) ./...

staticcheck: ## Run staticcheck (requires installation: go install honnef.co/go/tools/cmd/staticcheck@latest)
	@echo "Running staticcheck..."
	@which $(STATICCHECK) > /dev/null || (echo "staticcheck not found. Install with: go install honnef.co/go/tools/cmd/staticcheck@latest" && exit 1)
	$(STATICCHECK) ./...

clean: ## Remove build artifacts
	@echo "Cleaning build directory..."
	rm -rf $(BUILD_DIR)

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
