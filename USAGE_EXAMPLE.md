# Usage Examples

## Quick Start

```bash
# 1. Set your GitHub token
export GITHUB_TOKEN=ghp_your_token_here

# 2. Build the tool
make build

# 3. Run with sample PRs
make run > results.json
```

## Example: Filtering the Sample PRs

```bash
# Filter and save only passing PRs
./bin/pr-filter -input sample_prs.txt > filtered_prs.json

# See all PRs with their filter status
./bin/pr-filter -input sample_prs.txt -all > all_prs.json

# Verbose mode to see what's happening
./bin/pr-filter -input sample_prs.txt -verbose

# Custom criteria - more relaxed filters
./bin/pr-filter -input sample_prs.txt \
  -min-files 2 \
  -min-stars 100 \
  -min-lines 20 \
  -require-tests=false
```

## Example Output

### Filtered PRs (passing all criteria)

```json
[
  {
    "url": "https://github.com/abetlen/llama-cpp-python/pull/499",
    "title": "Migrate project to scikit-build-core",
    "number": 499,
    "repository": "abetlen/llama-cpp-python",
    "stars": 9931,
    "files_changed": 22,
    "lines_changed": 2261,
    "has_test_files": true,
    "resolved_issue": "https://github.com/abetlen/llama-cpp-python/issues/489",
    "issue_count": 1,
    "passes_filter": true,
    "fail_reasons": []
  }
]
```

### All PRs (with -all flag)

```json
[
  {
    "url": "https://github.com/abetlen/llama-cpp-python/pull/1257",
    "title": "add flag to disable EventSourceReponse ping messages",
    "number": 1257,
    "repository": "abetlen/llama-cpp-python",
    "stars": 9931,
    "files_changed": 2,
    "lines_changed": 16,
    "has_test_files": false,
    "resolved_issue": "https://github.com/abetlen/llama-cpp-python/issues/1256",
    "issue_count": 1,
    "passes_filter": false,
    "fail_reasons": [
      "Only 2 files changed (need 4+)",
      "No test files changed",
      "Only 16 lines changed (need 50+)"
    ]
  },
  {
    "url": "https://github.com/abetlen/llama-cpp-python/pull/499",
    "title": "Migrate project to scikit-build-core",
    "number": 499,
    "repository": "abetlen/llama-cpp-python",
    "stars": 9931,
    "files_changed": 22,
    "lines_changed": 2261,
    "has_test_files": true,
    "resolved_issue": "https://github.com/abetlen/llama-cpp-python/issues/489",
    "issue_count": 1,
    "passes_filter": true,
    "fail_reasons": []
  }
]
```

## Analyzing Results with jq

```bash
# Count passing PRs
cat filtered_prs.json | jq 'length'

# Get repositories with most qualifying PRs
cat filtered_prs.json | jq -r '.[].repository' | sort | uniq -c | sort -rn

# Get average lines changed in passing PRs
cat filtered_prs.json | jq '[.[].lines_changed] | add / length'

# List all resolved issues
cat filtered_prs.json | jq -r '.[].resolved_issue' | sort

# Find PRs with most files changed
cat filtered_prs.json | jq -r 'sort_by(.files_changed) | reverse | .[0:5] | .[] | "\(.files_changed) files: \(.url)"'

# Get failure statistics
cat all_prs.json | jq -r '.[] | select(.passes_filter == false) | .fail_reasons[]' | sort | uniq -c | sort -rn
```

## Common Scenarios

### 1. Finding PRs for a specific repository

```bash
# Filter sample PRs for specific repo
grep "llama-cpp-python" sample_prs.txt | ./bin/pr-filter
```

### 2. Batch processing with different criteria

```bash
# Very strict filters
./bin/pr-filter -input sample_prs.txt \
  -min-files 10 \
  -min-stars 1000 \
  -min-lines 200 > strict_results.json

# Relaxed filters
./bin/pr-filter -input sample_prs.txt \
  -min-files 2 \
  -min-stars 50 \
  -min-lines 20 \
  -require-tests=false > relaxed_results.json
```

### 3. Creating a custom input list

```bash
# Create a custom list
cat > custom_prs.txt << 'EOF'
# My interesting PRs to evaluate
https://github.com/owner/repo/pull/123
https://github.com/owner/repo/pull/456
# This one looks promising
https://github.com/another/repo/pull/789
EOF

# Filter them
./bin/pr-filter -input custom_prs.txt -verbose
```

## Expected Behavior

For the sample PRs provided, most will likely fail the filters because:

1. **Many small PRs** - Most change fewer than 4 files
2. **No test changes** - Many PRs don't modify test files
3. **Multiple issues** - Some PRs reference multiple issues or none
4. **Small changes** - Many PRs have fewer than 50 lines changed
5. **Low star count** - Some repositories have fewer than 200 stars

The strict criteria are designed to find **substantial, well-tested PRs that solve a single focused problem** in popular repositories.

## Troubleshooting

### Rate Limiting

If you're processing many PRs, you may hit GitHub's rate limit. The tool will show an error. Solutions:

1. Use an authenticated token (you should already be doing this)
2. Process PRs in smaller batches
3. Wait for rate limit to reset (shown in error message)

### Permission Errors

If you get 404 errors for public repos:
- Check that the PR URL is correct
- Verify the PR hasn't been deleted
- Ensure your token has the `public_repo` scope

For private repos:
- Your token needs the `repo` scope
- You must have access to the repository
