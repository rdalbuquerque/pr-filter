# PR Filter

A Go application to filter GitHub Pull Requests based on specific criteria.

## Filter Criteria

The application filters PRs based on the following requirements:

1. **4+ files changed** - PR must modify at least 4 files
2. **Test files included** - PR must change at least one test file
3. **200+ stars** - Repository must have at least 200 stars
4. **50+ lines of code** - PR must change at least 50 lines (additions + deletions)
5. **Single issue resolution** - PR must resolve exactly one issue

## Performance

- **Concurrent processing**: Uses 10 workers by default for parallel PR fetching
- **Fast**: Can process 100 PRs in under a minute (network and GitHub API dependent)
- **Configurable**: Adjust worker count with `-workers` flag (increase for speed, decrease to avoid rate limits)

## Installation

```bash
cd pr-filter
go mod download
go build -o pr-filter
```

## Usage

Set your GitHub token (required for API access):

```bash
export GITHUB_TOKEN=your_github_token_here
```

### Basic Usage

```bash
# From a file
./pr-filter -input sample_prs.txt

# Or via stdin
cat prs.txt | ./pr-filter

# Or interactively
./pr-filter
# Then paste URLs, one per line
# Press Ctrl+D when done
```

### Advanced Usage

```bash
# Sorting examples
./pr-filter -input sample_prs.txt -sort lines        # Sort by lines changed (default, descending)
./pr-filter -input sample_prs.txt -sort stars        # Sort by repository stars
./pr-filter -input sample_prs.txt -sort files        # Sort by files changed
./pr-filter -input sample_prs.txt -sort repository   # Sort alphabetically by repo name
./pr-filter -input sample_prs.txt -sort lines -sort-order asc  # Sort by lines, ascending

# Show all PRs (including those that failed filters)
./pr-filter -input sample_prs.txt -all

# Verbose output to see what's happening
./pr-filter -input sample_prs.txt -verbose

# Output as JSON instead of table
./pr-filter -input sample_prs.txt -output json > filtered_prs.json

# Use more workers for faster processing (default is 10)
./pr-filter -input sample_prs.txt -workers 20

# Use fewer workers to avoid rate limits
./pr-filter -input sample_prs.txt -workers 3

# Custom filter criteria
./pr-filter -input sample_prs.txt \
  -min-files 10 \
  -min-stars 500 \
  -min-lines 100 \
  -require-tests=false

# Combine options: relaxed filters, sort by stars, JSON output
./pr-filter -input sample_prs.txt \
  -min-files 2 \
  -min-stars 100 \
  -sort stars \
  -output json > top_starred.json

# Save table results to file
./pr-filter -input sample_prs.txt > filtered_prs.txt

# Use with Make
make run
```

### CLI Flags

**Input/Output:**
- `-input <file>` - Input file containing PR URLs (default: stdin)
- `-output <format>` - Output format: `table` or `json` (default: table)
- `-all` - Show all PRs including those that failed filters (default: false)
- `-verbose` - Show verbose progress output (default: false)

**Sorting:**
- `-sort <field>` - Sort by: `lines`, `files`, `stars`, `repository` (default: lines)
- `-sort-order <order>` - Sort order: `asc` or `desc` (default: desc)

**Performance:**
- `-workers <n>` - Number of concurrent workers (default: 10)

**Filter Criteria:**
- `-min-files <n>` - Minimum files changed (default: 4)
- `-min-stars <n>` - Minimum repository stars (default: 200)
- `-min-lines <n>` - Minimum lines changed (default: 50)
- `-require-tests` - Require test files to be changed (default: true)
- `-single-issue` - Require exactly one issue resolved (default: true)

## Input Format

One PR URL per line:

```
https://github.com/owner/repo/pull/123
https://github.com/owner/repo/pull/456
```

## Output Format

### Table Format (Default)

Results are displayed in a kubectl-style table, sorted by lines changed (descending) by default:

```
REPOSITORY                  STARS   FILES   LINES   RESOLVED ISSUE
abetlen/llama-cpp-python    9931    22      2261    https://github.com/abetlen/llama-cpp-python/issues/489
abhiTronix/vidgear          3421    11      284     https://github.com/abhiTronix/vidgear/issues/242
alteryx/featuretools        7234    8       156     https://github.com/alteryx/featuretools/issues/2701
```

**Sort Options:**
- `lines` - Total lines changed (additions + deletions) - **default**
- `files` - Number of files changed
- `stars` - Repository star count
- `repository` - Alphabetical by repository name

### JSON Format

Use `-output json` for JSON output:

```json
[
  {
    "url": "https://github.com/owner/repo/pull/123",
    "title": "PR Title",
    "number": 123,
    "repository": "owner/repo",
    "stars": 1500,
    "files_changed": 10,
    "lines_changed": 250,
    "has_test_files": true,
    "resolved_issue": "https://github.com/owner/repo/issues/100",
    "issue_count": 1,
    "passes_filter": true,
    "fail_reasons": []
  }
]
```

## Fields Explained

- `url`: Original PR URL
- `title`: PR title
- `number`: PR number
- `repository`: Repository in "owner/repo" format
- `stars`: Number of stars the repository has
- `files_changed`: Number of files modified in the PR
- `lines_changed`: Total lines added + deleted
- `has_test_files`: Whether the PR modifies test files
- `resolved_issue`: URL of the resolved issue (if exactly 1)
- `issue_count`: Number of issues referenced in PR body
- `passes_filter`: Whether the PR passes all criteria
- `fail_reasons`: Array of reasons why PR failed (empty if passed)

## Issue Detection

The tool detects issues referenced in the PR body using these patterns:

- `fixes #123`
- `closes #456`
- `resolves #789`
- `(Closes #123)`
- Full issue URLs: `https://github.com/owner/repo/issues/123`

Case-insensitive matching for keywords: fix, fixes, fixed, close, closes, closed, resolve, resolves, resolved.

## Example

```bash
# Create a sample input file
cat > prs.txt << 'EOF'
https://github.com/abetlen/llama-cpp-python/pull/1257
https://github.com/abetlen/llama-cpp-python/pull/499
EOF

# Run the filter
export GITHUB_TOKEN=ghp_your_token_here
cat prs.txt | ./pr-filter > filtered_results.json

# Check filtered count
cat filtered_results.json | jq 'length'
```

## Troubleshooting

**Error: GITHUB_TOKEN environment variable is required**
- Make sure to set your GitHub Personal Access Token

**Error: failed to fetch PR**
- Check that the PR URL is correct
- Verify your token has the required permissions
- Check if the repository is private (token needs access)

**No PRs in output**
- Check the criteria - they are intentionally strict
- Review the debug output (stderr) to see filtering stats

## TUI (Bubble Tea v2)

This repository includes a Bubble Tea v2 TUI for navigating cached PR data with filters and pagination.

### Build

```bash
go build -o bin/pr-filter-tui ./cmd/pr-filter-tui
```

### Run

```bash
# Fetch from Google Sheets if cache is empty (defaults from config)
export GITHUB_TOKEN=your_token_here
./bin/pr-filter-tui

# Optional overrides
./bin/pr-filter-tui -refresh
./bin/pr-filter-tui -cache /tmp/prs.db
```

The sheet must include columns named `taken` and `pr_link`. Only rows where `taken` is empty/false are fetched.

Config is stored at `~/.config/pr-filter/config.json` and is created on first run with defaults:
- `sheet_id`: `1WnGf8ULFHVpTjnpLz46DH-UrvOCLtkDmqilZLzaS4KM`
- `sheet_gid`: `886975217`
- `cache_path`: `~/.config/pr-filter/cache.db`
- `google_secret`: `client_secret_1047690774768-7u0fn9fkn61g2nhu1kcrsj0otdoobjtg.apps.googleusercontent.com.json`

### Controls

- `q` quit
- `f` filters
- `r` reset filters to defaults
- `c` clear filters
- `/` search
- `n`/`p` next/previous page
- `g`/`G` first/last page
- `s` cycle sort field (lines, files, stars, repository)
- `o` toggle sort order
- `l` logs
- `enter` view PR details
- `x` toggle checked
- `m` toggle saved
- `v` toggle view (active/saved/checked)
