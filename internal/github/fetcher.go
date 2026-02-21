package github

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	gh "github.com/google/go-github/v58/github"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
	"golang.org/x/oauth2"
)

// FetchOptions configures concurrent PR fetching.
type FetchOptions struct {
	Workers  int
	MaxPRs   int
	Progress func(done, total int)
}

// FetchPRs fetches multiple PRs concurrently (Pass 1 hydration).
func FetchPRs(ctx context.Context, prURLs []string, token string, opts FetchOptions) ([]prdata.PRInfo, []error) {
	if opts.Workers <= 0 {
		opts.Workers = 10
	}

	httpClient, err := newGitHubClient(ctx, token)
	if err != nil {
		return nil, []error{err}
	}
	client := gh.NewClient(httpClient)

	if opts.MaxPRs > 0 && opts.MaxPRs < len(prURLs) {
		prURLs = prURLs[:opts.MaxPRs]
	}

	type job struct {
		index int
		url   string
	}
	type result struct {
		index int
		info  prdata.PRInfo
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
				info, err := FetchPR(ctx, client, j.url)
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

	ordered := make([]prdata.PRInfo, len(prURLs))
	errors := make([]error, 0)
	total := len(prURLs)
	done := 0
	for res := range results {
		done++
		if opts.Progress != nil {
			opts.Progress(done, total)
		}
		if res.err != nil {
			errors = append(errors, res.err)
			continue
		}
		ordered[res.index] = res.info
	}

	filtered := make([]prdata.PRInfo, 0, len(prURLs)-len(errors))
	for _, pr := range ordered {
		if pr.URL == "" {
			continue
		}
		filtered = append(filtered, pr)
	}

	return filtered, errors
}

// FetchPR fetches a single PR (Pass 1: basic info).
func FetchPR(ctx context.Context, client *gh.Client, prURL string) (prdata.PRInfo, error) {
	owner, repo, number, err := ParsePRURL(prURL)
	if err != nil {
		return prdata.PRInfo{}, err
	}

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return prdata.PRInfo{}, fmt.Errorf("fetch PR: %w", err)
	}

	filesChanged := pr.GetChangedFiles()
	linesChanged := pr.GetAdditions() + pr.GetDeletions()

	issues := ExtractIssues(pr.GetBody(), owner, repo)
	issueCount := len(issues)
	var resolvedIssue string
	if issueCount == 1 {
		resolvedIssue = issues[0]
	}

	info := prdata.PRInfo{
		URL:           prURL,
		Title:         pr.GetTitle(),
		Number:        number,
		Repository:    fmt.Sprintf("%s/%s", owner, repo),
		Stars:         0,
		StarsKnown:    false,
		FilesChanged:  filesChanged,
		LinesChanged:  linesChanged,
		HasTestFiles:  false,
		HasTestKnown:  false,
		Hydration:     1,
		ResolvedIssue: resolvedIssue,
		IssueCount:    issueCount,
		FailReasons:   make([]string, 0),
		Taken:         false,
	}

	prdata.ApplyDefaultFilters(&info)
	return info, nil
}

// HydratePRPass2 fetches stars and test file info for a previously fetched PR.
func HydratePRPass2(ctx context.Context, prInfo prdata.PRInfo, token string) (prdata.PRInfo, error) {
	httpClient, err := newGitHubClient(ctx, token)
	if err != nil {
		return prdata.PRInfo{}, err
	}

	client := gh.NewClient(httpClient)
	owner, repo, number, err := ParsePRURL(prInfo.URL)
	if err != nil {
		return prdata.PRInfo{}, err
	}

	if !prInfo.StarsKnown {
		repository, _, err := client.Repositories.Get(ctx, owner, repo)
		if err != nil {
			return prdata.PRInfo{}, fmt.Errorf("fetch repository: %w", err)
		}
		prInfo.Stars = repository.GetStargazersCount()
		prInfo.StarsKnown = true
	}

	if !prInfo.HasTestKnown {
		files, err := FetchAllPRFiles(ctx, client, owner, repo, number)
		if err != nil {
			return prdata.PRInfo{}, fmt.Errorf("fetch files: %w", err)
		}
		prInfo.HasTestFiles = CheckForTestFiles(files)
		prInfo.HasTestKnown = true
	}

	prInfo.Hydration = 2
	prInfo.FailReasons = nil
	prdata.ApplyDefaultFilters(&prInfo)
	return prInfo, nil
}

// NewGitHubClientFromToken creates a *gh.Client from a token string.
func NewGitHubClientFromToken(ctx context.Context, token string) (*gh.Client, error) {
	httpClient, err := newGitHubClient(ctx, token)
	if err != nil {
		return nil, err
	}
	return gh.NewClient(httpClient), nil
}

func newGitHubClient(ctx context.Context, token string) (*http.Client, error) {
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		return oauth2.NewClient(ctx, ts), nil
	}
	auth := NewGitHubAuth("")
	return auth.GetClient(ctx)
}
