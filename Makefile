.PHONY: build test test-unit test-integration clean run install-caddy lint coverage help release release-snapshot

# Variables
BINARY_NAME=fwrd
BINARY_PATH=./$(BINARY_NAME)
MAIN_PATH=cmd/rss/main.go
COVERAGE_FILE=coverage.out
COVERAGE_HTML=coverage.html

# Default target
all: build

## help: Display this help message
help:
	@echo "fwrd"
	@echo ""
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

## build: Build fwrd binary
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_PATH) $(MAIN_PATH)
	@echo "Build complete: $(BINARY_PATH)"

## run: Build and run fwrd
run: build
	@echo "Running $(BINARY_NAME)..."
	@$(BINARY_PATH)

## test: Run all tests (unit and integration)
test: test-unit test-integration

## test-unit: Run unit tests only
test-unit:
	@echo "Running unit tests..."
	@go test -v ./internal/...

## test-integration: Run integration tests (requires Caddy)
test-integration: check-caddy
	@echo "Running integration tests..."
	@cd test/integration && go test -v -timeout 30s

## test-race: Run tests with race condition detection
test-race:
	@echo "Running tests with race detection..."
	@go test -race -v ./...

## coverage: Generate test coverage report
coverage:
	@echo "Generating coverage report..."
	@go test -coverprofile=$(COVERAGE_FILE) ./internal/...
	@go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report generated: $(COVERAGE_HTML)"
	@go tool cover -func=$(COVERAGE_FILE)

## lint: Run Go linters
lint:
	@echo "Running linters..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Running go vet instead..."; \
		go vet ./...; \
	fi
	@echo "Running gofmt..."
	@gofmt -l -s .

## fmt: Format Go code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@gofmt -s -w .

## clean: Remove build artifacts and test cache
clean:
	@echo "Cleaning build artifacts..."
	@rm -f $(BINARY_PATH)
	@rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)
	@go clean -testcache
	@echo "Clean complete"

## install: Install fwrd to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME) to GOPATH/bin..."
	@go install $(MAIN_PATH)
	@echo "Installation complete"

## deps: Download and tidy dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy
	@echo "Dependencies updated"

## check-caddy: Check if Caddy is installed
check-caddy:
	@command -v caddy > /dev/null || (echo "Error: Caddy is not installed. Please install Caddy for integration tests." && exit 1)

## install-caddy: Install Caddy (macOS with Homebrew)
install-caddy:
	@echo "Installing Caddy..."
	@if [ "$$(uname)" = "Darwin" ]; then \
		if command -v brew > /dev/null; then \
			brew install caddy; \
		else \
			echo "Homebrew not found. Please install Caddy manually."; \
			echo "Visit: https://caddyserver.com/docs/install"; \
			exit 1; \
		fi \
	else \
		echo "Please install Caddy manually for your platform."; \
		echo "Visit: https://caddyserver.com/docs/install"; \
		exit 1; \
	fi

## start-test-server: Start Caddy test server
start-test-server: check-caddy
	@echo "Starting Caddy test server..."
	@cd test/fixtures && caddy run --config Caddyfile &
	@echo "Test server started on http://localhost:8080"
	@echo "Run 'make stop-test-server' to stop"

## stop-test-server: Stop Caddy test server
stop-test-server:
	@echo "Stopping Caddy test server..."
	@pkill -f "caddy run" || true
	@echo "Test server stopped"

## dev: Run fwrd in development mode with auto-rebuild
dev:
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air not installed. Install with: go install github.com/cosmtrek/air@latest"; \
		echo "Running without auto-reload..."; \
		make run; \
	fi

## bench: Run benchmarks
bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

## mod-update: Update all dependencies to latest versions
mod-update:
	@echo "Updating dependencies to latest versions..."
	@go get -u ./...
	@go mod tidy
	@echo "Dependencies updated"

## check: Run all checks (lint, test, build)
check: lint test build
	@echo "All checks passed!"

# CI/CD targets
## ci: Run CI pipeline
ci: deps lint test-unit build
	@echo "CI pipeline complete"

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	@docker build -t $(BINARY_NAME):latest .

## docker-run: Run fwrd in Docker
docker-run: docker-build
	@echo "Running fwrd in Docker..."
	@docker run -it --rm $(BINARY_NAME):latest

## release: Create a new release using GoReleaser
release:
	@echo "Creating release with GoReleaser..."
	@goreleaser release --clean

## release-snapshot: Create a snapshot release using GoReleaser
release-snapshot:
	@echo "Creating snapshot release with GoReleaser..."
	@goreleaser release --snapshot --clean