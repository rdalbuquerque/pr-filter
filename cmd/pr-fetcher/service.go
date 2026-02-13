package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v58/github"
	ghpkg "github.com/revelo/pr-filter/internal/github"
	"github.com/revelo/pr-filter/internal/prdata"
	"github.com/revelo/pr-filter/internal/sheets"
	"golang.org/x/oauth2"
)

type service struct {
	cfg       serviceConfig
	state     *prdata.DataFile
	mu        sync.Mutex
	rateReset time.Time // when GitHub rate limit resets
}

// runSheetPoller is Loop 1: polls Google Sheets on an interval.
func (s *service) runSheetPoller(ctx context.Context) {
	// Run immediately on start
	s.pollSheet(ctx)

	ticker := time.NewTicker(s.cfg.SheetPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollSheet(ctx)
		}
	}
}

func (s *service) pollSheet(ctx context.Context) {
	log.Printf("[sheet] polling Google Sheets...")

	rows, err := sheets.LoadPRRowsFromSheet(ctx, sheets.SheetOptions{
		SheetID:   s.cfg.SheetID,
		SheetGID:  s.cfg.SheetGID,
		CredsPath: s.cfg.GoogleSecret,
		TokenPath: s.cfg.GoogleToken,
	})
	if err != nil {
		log.Printf("[sheet] error: %v", err)
		return
	}

	log.Printf("[sheet] got %d rows from sheet", len(rows))

	s.mu.Lock()
	defer s.mu.Unlock()

	// Build index of existing PRs
	existing := make(map[string]*prdata.PRInfo)
	for i := range s.state.PRs {
		existing[s.state.PRs[i].URL] = &s.state.PRs[i]
	}

	// Track which URLs are still in the sheet
	inSheet := make(map[string]bool)

	newPRs := make([]prdata.PRInfo, 0, len(rows))
	for _, row := range rows {
		if inSheet[row.PRLink] {
			continue // skip duplicate rows in sheet
		}
		inSheet[row.PRLink] = true

		if ex, ok := existing[row.PRLink]; ok {
			// Keep hydration data, update Taken status
			ex.Taken = row.Taken
			newPRs = append(newPRs, *ex)
		} else {
			// New PR from sheet - lightweight entry
			newPRs = append(newPRs, makeLightweightPR(row))
		}
	}

	// PRs that disappeared from sheet: mark as taken
	for url, pr := range existing {
		if !inSheet[url] {
			pr.Taken = true
			newPRs = append(newPRs, *pr)
		}
	}

	s.state.PRs = newPRs
	s.state.SheetPollAt = time.Now()
	s.updateStats()
	s.save()

	log.Printf("[sheet] merged: %d total PRs, %d from sheet", len(newPRs), len(rows))
}

// runHydrator is Loop 2: hydrates PRs from GitHub API.
func (s *service) runHydrator(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.HydrationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.hydrateBatch(ctx)
		}
	}
}

func (s *service) hydrateBatch(ctx context.Context) {
	// Check if we're in a rate limit cooldown
	if wait := time.Until(s.rateReset); wait > 0 {
		log.Printf("[hydrator] rate limited, waiting %s", wait.Truncate(time.Second))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	s.mu.Lock()
	batch := s.pickBatch()
	s.mu.Unlock()

	if len(batch) == 0 {
		return
	}

	p1, p2 := 0, 0
	for _, pr := range batch {
		if pr.Hydration == 0 {
			p1++
		} else {
			p2++
		}
	}
	log.Printf("[hydrator] processing %d PRs (pass1: %d, pass2: %d)", len(batch), p1, p2)

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: s.cfg.GitHubToken})
	httpClient := oauth2.NewClient(ctx, ts)
	client := gh.NewClient(httpClient)

	for _, pr := range batch {
		if ctx.Err() != nil {
			return
		}

		updated, err := hydrateSingle(ctx, client, pr)
		if err != nil {
			// Check if this is a rate limit error
			if rateLimitErr, ok := err.(*gh.RateLimitError); ok {
				s.rateReset = rateLimitErr.Rate.Reset.Time
				wait := time.Until(s.rateReset)
				log.Printf("[hydrator] rate limited! %d/%d used, resets in %s",
					rateLimitErr.Rate.Limit-rateLimitErr.Rate.Remaining,
					rateLimitErr.Rate.Limit,
					wait.Truncate(time.Second))
				break
			}
			if abuseErr, ok := err.(*gh.AbuseRateLimitError); ok {
				retryAfter := abuseErr.GetRetryAfter()
				if retryAfter == 0 {
					retryAfter = 60 * time.Second
				}
				s.rateReset = time.Now().Add(retryAfter)
				log.Printf("[hydrator] abuse rate limit! retrying after %s", retryAfter)
				break
			}
			// Mark as permanent error — log full details and move on
			log.Printf("[hydrator] permanent error for %s: %v", pr.URL, err)
			s.mu.Lock()
			for i := range s.state.PRs {
				if s.state.PRs[i].URL == pr.URL {
					s.state.PRs[i].HydrationError = err.Error()
					break
				}
			}
			s.mu.Unlock()
			continue
		}

		s.mu.Lock()
		for i := range s.state.PRs {
			if s.state.PRs[i].URL == updated.URL {
				s.state.PRs[i] = updated
				break
			}
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	s.updateStats()
	s.save()
	s.mu.Unlock()

	// Check remaining rate limit after batch
	rl, _, err := client.RateLimit.Get(ctx)
	if err == nil && rl.Core != nil {
		remaining := rl.Core.Remaining
		limit := rl.Core.Limit
		log.Printf("[hydrator] rate limit: %d/%d remaining, resets %s",
			remaining, limit, rl.Core.Reset.Time.Format(time.Kitchen))
		if remaining < 100 {
			s.rateReset = rl.Core.Reset.Time
			log.Printf("[hydrator] low rate limit (%d remaining), pausing until reset", remaining)
		}
	}
}

const maxRetries = 3

func (s *service) pickBatch() []prdata.PRInfo {
	var pass1, pass2 []prdata.PRInfo

	for _, pr := range s.state.PRs {
		if pr.Taken || pr.HydrationError != "" || pr.HydrationRetries >= maxRetries {
			continue
		}
		switch pr.Hydration {
		case 0:
			pass1 = append(pass1, pr)
		case 1:
			pass2 = append(pass2, pr)
		}
	}

	// Mix pass1 and pass2: pass1 first, fill rest with pass2
	batch := make([]prdata.PRInfo, 0, s.cfg.HydrationBatch)
	if len(pass1) > s.cfg.HydrationBatch {
		batch = append(batch, pass1[:s.cfg.HydrationBatch]...)
	} else {
		batch = append(batch, pass1...)
	}
	remaining := s.cfg.HydrationBatch - len(batch)
	if remaining > 0 && len(pass2) > 0 {
		if len(pass2) > remaining {
			batch = append(batch, pass2[:remaining]...)
		} else {
			batch = append(batch, pass2...)
		}
	}

	return batch
}

func hydrateSingle(ctx context.Context, client *gh.Client, pr prdata.PRInfo) (prdata.PRInfo, error) {
	if pr.Hydration == 0 {
		return hydratePass1(ctx, client, pr)
	}
	return hydratePass2(ctx, client, pr)
}

func hydratePass1(ctx context.Context, client *gh.Client, pr prdata.PRInfo) (prdata.PRInfo, error) {
	owner, repo, number, err := ghpkg.ParsePRURL(pr.URL)
	if err != nil {
		return pr, err
	}

	ghPR, resp, err := client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return pr, err
	}
	_ = resp

	pr.Number = number
	pr.Title = ghPR.GetTitle()
	pr.FilesChanged = ghPR.GetChangedFiles()
	pr.LinesChanged = ghPR.GetAdditions() + ghPR.GetDeletions()

	issues := ghpkg.ExtractIssues(ghPR.GetBody(), owner, repo)
	pr.IssueCount = len(issues)
	if len(issues) == 1 {
		pr.ResolvedIssue = issues[0]
	}

	pr.Hydration = 1
	pr.FailReasons = nil
	prdata.ApplyDefaultFilters(&pr)
	return pr, nil
}

func hydratePass2(ctx context.Context, client *gh.Client, pr prdata.PRInfo) (prdata.PRInfo, error) {
	owner, repo, number, err := ghpkg.ParsePRURL(pr.URL)
	if err != nil {
		return pr, err
	}

	if !pr.StarsKnown {
		repository, _, err := client.Repositories.Get(ctx, owner, repo)
		if err != nil {
			return pr, err
		}
		pr.Stars = repository.GetStargazersCount()
		pr.StarsKnown = true
	}

	if !pr.HasTestKnown {
		files, err := ghpkg.FetchAllPRFiles(ctx, client, owner, repo, number)
		if err != nil {
			return pr, err
		}
		pr.HasTestFiles = ghpkg.CheckForTestFiles(files)
		pr.HasTestKnown = true
	}

	pr.Hydration = 2
	pr.FailReasons = nil
	prdata.ApplyDefaultFilters(&pr)
	return pr, nil
}

func (s *service) updateStats() {
	stats := prdata.DataStats{}
	for _, pr := range s.state.PRs {
		if !pr.Taken {
			stats.TotalFromSheet++
		} else {
			stats.TakenCount++
		}
		if pr.Hydration >= 1 {
			stats.HydratedPass1++
		}
		if pr.Hydration >= 2 {
			stats.HydratedPass2++
		}
	}
	s.state.Stats = stats
}

func (s *service) save() {
	s.state.UpdatedAt = time.Now()
	if err := prdata.SaveDataFile(s.cfg.OutputPath, s.state); err != nil {
		log.Printf("[save] error: %v", err)
	}
}

func makeLightweightPR(row prdata.SheetPRRow) prdata.PRInfo {
	repo := strings.TrimSpace(row.Repo)
	parts := strings.Split(row.PRLink, "/")
	if len(parts) >= 5 {
		repo = parts[3] + "/" + parts[4]
	}
	if repo == "" {
		repo = "unknown/unknown"
	}

	var number int
	if _, _, n, err := ghpkg.ParsePRURL(row.PRLink); err == nil {
		number = n
	}

	return prdata.PRInfo{
		URL:          row.PRLink,
		Title:        "(not fetched yet)",
		Number:       number,
		Repository:   repo,
		FilesChanged: row.FilesChanged,
		LinesChanged: row.LinesChanged,
		Hydration:    0,
		StarsKnown:   false,
		HasTestKnown: false,
		Taken:        row.Taken,
		FailReasons:  []string{"Not fully fetched yet"},
	}
}

func setupSheetsClient(ctx context.Context, credsPath, tokenPath string) (*http.Client, error) {
	return sheets.SheetsClient(ctx, credsPath, tokenPath)
}

