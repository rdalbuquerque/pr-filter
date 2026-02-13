.PHONY: build-fetcher build-tui docker-up docker-down setup-google-auth clean test deps help

OUTPUT_DIR=bin

# Build the data-gathering service
build-fetcher:
	@echo "Building pr-fetcher..."
	@mkdir -p $(OUTPUT_DIR)
	go build -o $(OUTPUT_DIR)/pr-fetcher ./cmd/pr-fetcher
	@echo "Build complete: $(OUTPUT_DIR)/pr-fetcher"

# Build the TUI application
build-tui:
	@echo "Building pr-filter-tui..."
	@mkdir -p $(OUTPUT_DIR)
	go build -o $(OUTPUT_DIR)/pr-filter-tui ./cmd/pr-filter-tui
	@echo "Build complete: $(OUTPUT_DIR)/pr-filter-tui"

# Build both
build: build-fetcher build-tui

# Run the fetcher locally (no Docker)
run-fetcher: build-fetcher
	$(OUTPUT_DIR)/pr-fetcher

# Run the TUI against data file
run-tui: build-tui
	$(OUTPUT_DIR)/pr-filter-tui --data data/prs.json

# Docker targets
docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

# One-time Google OAuth setup
setup-google-auth:
	SHEET_ID=$${SHEET_ID} \
	GOOGLE_SECRET=$${GOOGLE_SECRET:-config/client_secret.json} \
	GOOGLE_TOKEN=$${GOOGLE_TOKEN:-config/google-token.json} \
	GITHUB_TOKEN=$${GITHUB_TOKEN:-placeholder} \
	go run ./cmd/pr-fetcher --setup

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

# Show help
help:
	@echo "Available targets:"
	@echo "  build-fetcher      - Build the data-gathering service"
	@echo "  build-tui          - Build the TUI application"
	@echo "  build              - Build both binaries"
	@echo "  run-fetcher        - Run fetcher locally"
	@echo "  run-tui            - Run TUI against data/prs.json"
	@echo "  docker-up          - Start fetcher in Docker"
	@echo "  docker-down        - Stop Docker container"
	@echo "  docker-logs        - Follow Docker logs"
	@echo "  setup-google-auth  - One-time Google OAuth setup"
	@echo "  deps               - Download dependencies"
	@echo "  clean              - Remove build artifacts"
	@echo "  test               - Run tests"
	@echo ""
	@echo "Environment variables:"
	@echo "  GITHUB_TOKEN  - Required for GitHub API access"
	@echo "  SHEET_ID      - Google Sheet ID"
	@echo "  SHEET_GID     - Sheet tab GID"
	@echo "  GOOGLE_SECRET - Path to Google OAuth client secret"
	@echo "  GOOGLE_TOKEN  - Path to Google OAuth token cache"
