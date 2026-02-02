.PHONY: build build-tui run run-tui clean test help

# Binary name
BINARY=pr-filter
OUTPUT_DIR=bin

# Build the application
build:
	@echo "Building $(BINARY)..."
	@mkdir -p $(OUTPUT_DIR)
	go build -o $(OUTPUT_DIR)/$(BINARY) .
	@echo "Build complete: $(OUTPUT_DIR)/$(BINARY)"

# Build the TUI application
build-tui:
	@echo "Building pr-filter-tui..."
	@mkdir -p $(OUTPUT_DIR)
	go build -o $(OUTPUT_DIR)/pr-filter-tui ./cmd/pr-filter-tui
	@echo "Build complete: $(OUTPUT_DIR)/pr-filter-tui"

# Run with sample PRs (table output)
run: build
	@if [ -z "$$GITHUB_TOKEN" ]; then \
		echo "Error: GITHUB_TOKEN environment variable not set"; \
		exit 1; \
	fi
	$(OUTPUT_DIR)/$(BINARY) -input sample_prs.txt

# Run with sample PRs (JSON output)
run-json: build
	@if [ -z "$$GITHUB_TOKEN" ]; then \
		echo "Error: GITHUB_TOKEN environment variable not set"; \
		exit 1; \
	fi
	$(OUTPUT_DIR)/$(BINARY) -input sample_prs.txt -output json

# Run with custom input
run-custom: build
	@echo "Running $(BINARY) (paste PR URLs, Ctrl+D to finish)..."
	@if [ -z "$$GITHUB_TOKEN" ]; then \
		echo "Error: GITHUB_TOKEN environment variable not set"; \
		exit 1; \
	fi
	$(OUTPUT_DIR)/$(BINARY)

# Run the TUI (expects -cache or -data)
run-tui: build-tui
	$(OUTPUT_DIR)/pr-filter-tui

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(OUTPUT_DIR)
	go clean

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Install the binary to $GOPATH/bin
install:
	@echo "Installing $(BINARY)..."
	go install .

# Show help
help:
	@echo "Available targets:"
	@echo "  build       - Build the application"
	@echo "  build-tui   - Build the TUI application"
	@echo "  run         - Run with sample_prs.txt (table output)"
	@echo "  run-tui     - Run the TUI (requires -cache or -data)"
	@echo "  run-json    - Run with sample_prs.txt (JSON output)"
	@echo "  run-custom  - Run with custom input (stdin)"
	@echo "  deps        - Download dependencies"
	@echo "  clean       - Remove build artifacts"
	@echo "  test        - Run tests"
	@echo "  install     - Install to GOPATH/bin"
	@echo "  help        - Show this help"
	@echo ""
	@echo "Environment variables:"
	@echo "  GITHUB_TOKEN - Required for GitHub API access"
