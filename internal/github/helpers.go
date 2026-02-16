package github

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	gh "github.com/google/go-github/v58/github"
)

// ParsePRURL extracts owner, repo, and PR number from a GitHub PR URL.
func ParsePRURL(url string) (owner, repo string, number int, err error) {
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid PR URL: %s", url)
	}

	owner = matches[1]
	repo = matches[2]
	fmt.Sscanf(matches[3], "%d", &number)
	return owner, repo, number, nil
}

// ParseIssueURL extracts owner, repo, and issue number from a GitHub issue URL.
func ParseIssueURL(url string) (owner, repo string, number int, err error) {
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid issue URL: %s", url)
	}

	owner = matches[1]
	repo = matches[2]
	fmt.Sscanf(matches[3], "%d", &number)
	return owner, repo, number, nil
}

// CheckForTestFiles returns true if any of the given files are test files.
func CheckForTestFiles(files []*gh.CommitFile) bool {
	testPatterns := []string{
		"test",
		"spec",
		"_test.go",
		"_test.py",
		".test.js",
		".test.ts",
		".spec.js",
		".spec.ts",
	}

	for _, file := range files {
		filename := strings.ToLower(file.GetFilename())
		for _, pattern := range testPatterns {
			if strings.Contains(filename, pattern) {
				return true
			}
		}
	}
	return false
}

// FetchAllPRFiles retrieves all files for a PR with pagination.
func FetchAllPRFiles(ctx context.Context, client *gh.Client, owner, repo string, number int) ([]*gh.CommitFile, error) {
	files := make([]*gh.CommitFile, 0)
	page := 1
	for {
		opts := &gh.ListOptions{PerPage: 100, Page: page}
		batch, resp, err := client.PullRequests.ListFiles(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, err
		}
		files = append(files, batch...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return files, nil
}

// ExtractIssues finds issue references in a PR body.
func ExtractIssues(body, owner, repo string) []string {
	issues := make([]string, 0)
	patterns := []string{
		`(?i)(?:fix|fixes|fixed|close|closes|closed|resolve|resolves|resolved)\s+#(\d+)`,
		`(?i)(?:fix|fixes|fixed|close|closes|closed|resolve|resolves|resolved)\s+https://github\.com/[^/]+/[^/]+/issues/(\d+)`,
		`\(Closes\s+#(\d+)\)`,
	}

	seen := make(map[string]bool)
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(body, -1)
		for _, match := range matches {
			if len(match) > 1 {
				issueNum := match[1]
				issueURL := fmt.Sprintf("https://github.com/%s/%s/issues/%s", owner, repo, issueNum)
				if !seen[issueURL] {
					issues = append(issues, issueURL)
					seen[issueURL] = true
				}
			}
		}
	}
	return issues
}
