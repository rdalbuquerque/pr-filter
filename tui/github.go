package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

type FetchOptions struct {
	Workers int
	MaxPRs  int
}

func FetchPRs(ctx context.Context, prURLs []string, token string, opts FetchOptions) ([]PRInfo, []error) {
	if opts.Workers <= 0 {
		opts.Workers = 10
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	if opts.MaxPRs <= 0 {
		if maxPRs, err := estimateMaxPRs(ctx, client); err == nil && maxPRs > 0 && maxPRs < len(prURLs) {
			prURLs = prURLs[:maxPRs]
		}
	} else if opts.MaxPRs < len(prURLs) {
		prURLs = prURLs[:opts.MaxPRs]
	}

	type job struct {
		index int
		url   string
	}
	type result struct {
		index int
		info  PRInfo
		err   error
	}

	jobs := make(chan job, len(prURLs))
	results := make(chan result, len(prURLs))

	var wg sync.WaitGroup
	for w := 0; w < opts.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				info, err := fetchPR(ctx, client, j.url)
				results <- result{index: j.index, info: info, err: err}
			}
		}()
	}

	for i, url := range prURLs {
		jobs <- job{index: i, url: url}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	ordered := make([]PRInfo, len(prURLs))
	errors := make([]error, 0)
	for res := range results {
		if res.err != nil {
			errors = append(errors, res.err)
			continue
		}
		ordered[res.index] = res.info
	}

	filtered := make([]PRInfo, 0, len(prURLs)-len(errors))
	for _, pr := range ordered {
		if pr.URL == "" {
			continue
		}
		filtered = append(filtered, pr)
	}

	return filtered, errors
}

func estimateMaxPRs(ctx context.Context, client *github.Client) (int, error) {
	limits, _, err := client.RateLimit.Get(ctx)
	if err != nil {
		return 0, err
	}
	remaining := limits.Core.Remaining
	if remaining <= 0 {
		return 0, fmt.Errorf("rate limit exhausted")
	}
	// Each PR needs roughly 3 requests: PR, repo, files.
	perPR := 3
	max := remaining / perPR
	if max < 1 {
		return 0, fmt.Errorf("rate limit too low")
	}
	return max, nil
}

func fetchPR(ctx context.Context, client *github.Client, prURL string) (PRInfo, error) {
	owner, repo, number, err := parsePRURL(prURL)
	if err != nil {
		return PRInfo{}, err
	}

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return PRInfo{}, fmt.Errorf("fetch PR: %w", err)
	}

	repository, _, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return PRInfo{}, fmt.Errorf("fetch repository: %w", err)
	}

	files, _, err := client.PullRequests.ListFiles(ctx, owner, repo, number, nil)
	if err != nil {
		return PRInfo{}, fmt.Errorf("fetch files: %w", err)
	}

	filesChanged := len(files)
	linesChanged := pr.GetAdditions() + pr.GetDeletions()
	hasTestFiles := checkForTestFiles(files)

	issues := extractIssues(pr.GetBody(), owner, repo)
	issueCount := len(issues)
	var resolvedIssue string
	if issueCount == 1 {
		resolvedIssue = issues[0]
	}

	info := PRInfo{
		URL:           prURL,
		Title:         pr.GetTitle(),
		Number:        number,
		Repository:    fmt.Sprintf("%s/%s", owner, repo),
		Stars:         repository.GetStargazersCount(),
		FilesChanged:  filesChanged,
		LinesChanged:  linesChanged,
		HasTestFiles:  hasTestFiles,
		ResolvedIssue: resolvedIssue,
		IssueCount:    issueCount,
		FailReasons:   make([]string, 0),
		Taken:         false,
	}

	applyDefaultFilters(&info)
	return info, nil
}

func applyDefaultFilters(info *PRInfo) {
	criteria := DefaultFilters()

	if info.FilesChanged < criteria.MinFiles {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d files changed (need %d+)", info.FilesChanged, criteria.MinFiles))
	}
	if criteria.RequireTestFiles && !info.HasTestFiles {
		info.FailReasons = append(info.FailReasons, "No test files changed")
	}
	if info.Stars < criteria.MinStars {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d stars (need %d+)", info.Stars, criteria.MinStars))
	}
	if info.LinesChanged < criteria.MinLines {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d lines changed (need %d+)", info.LinesChanged, criteria.MinLines))
	}
	if criteria.RequireSingleIssue && info.IssueCount != 1 {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Resolves %d issues (need exactly 1)", info.IssueCount))
	}
	info.PassesFilter = len(info.FailReasons) == 0
}

func parsePRURL(url string) (owner, repo string, number int, err error) {
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

func checkForTestFiles(files []*github.CommitFile) bool {
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

func extractIssues(body, owner, repo string) []string {
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
