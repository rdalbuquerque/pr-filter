# PR Filter

A TUI application for browsing and filtering GitHub Pull Requests, with AI-powered evaluation. Data is fetched from Google Sheets, hydrated via the GitHub API, and published to Azure Blob Storage. The TUI reads from Azure by default — no credentials needed to browse.

## Install

```sh
curl -sSL https://raw.githubusercontent.com/rdalbuquerque/pr-filter/main/install.sh | sh
```

This installs the `pr-filter-tui` binary to `/usr/local/bin`. Customize with:

```sh
# Install to a different directory
curl -sSL https://raw.githubusercontent.com/rdalbuquerque/pr-filter/main/install.sh | INSTALL_DIR=~/.local/bin sh

# Install a specific version
curl -sSL https://raw.githubusercontent.com/rdalbuquerque/pr-filter/main/install.sh | VERSION=v0.1.0 sh
```

Or download binaries directly from [Releases](https://github.com/rdalbuquerque/pr-filter/releases).

## Usage

```sh
# Browse PRs (fetches data from Azure, no credentials needed)
pr-filter-tui

# Optional: set GitHub token for fetching PR diffs and issue bodies
export GITHUB_TOKEN=ghp_...
pr-filter-tui

# Use a local data file instead of Azure
pr-filter-tui --data data/prs.json
```

## TUI Controls

| Key | Action |
|-----|--------|
| `q` | Quit |
| `enter` | View PR details |
| `f` | Filters |
| `r` | Reset filters to defaults |
| `c` | Clear filters |
| `/` | Search |
| `n` / `p` | Next / previous page |
| `g` / `G` | First / last page |
| `s` | Cycle sort field (lines, files, stars, repository) |
| `o` | Toggle sort order |
| `x` | Toggle checked |
| `m` | Toggle saved/favorite |
| `v` | Toggle view (active / saved / checked) |
| `R` | Reload data from Azure |
| `l` | Logs |
| `tab` | Switch detail tabs (Diff / Issue) |
| `d` | Toggle diff layout (unified / side-by-side) |

## Architecture

The system has three components:

- **pr-fetcher** — reads PR URLs from Google Sheets, hydrates via GitHub API, publishes `prs.json` to Azure Blob Storage
- **pr-evaluator** — runs AI evaluation on PRs using Claude, publishes `ai-evaluations.json` to Azure Blob Storage
- **pr-filter-tui** — terminal UI that reads from Azure (or local files) and lets you browse, filter, and evaluate PRs

### Running the backend services

```sh
# Copy and fill in environment variables
cp .env.example .env

# Run both services in Docker
docker compose up -d
```

### Building from source

```sh
make build          # Build all binaries
make build-tui      # Build TUI only
make build-fetcher  # Build fetcher only
make build-evaluator # Build evaluator only
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | Optional | GitHub token for fetching PR diffs/issues in TUI |
| `AZURE_STORAGE_ACCOUNT` | Optional | Storage account name (default: `prfilterdata`) |
| `AZURE_CONTAINER` | Optional | Blob container name (default: `prdata`) |
| `SHEET_ID` | Backend | Google Sheet ID |
| `SHEET_GID` | Backend | Sheet tab GID |
| `ANTHROPIC_API_KEY` | Backend | For AI evaluator |
| `AZURE_STORAGE_KEY` | Backend | For publishing to Azure |
