package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

type PRInfo struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Number        int      `json:"number"`
	Repository    string   `json:"repository"`
	Stars         int      `json:"stars"`
	FilesChanged  int      `json:"files_changed"`
	LinesChanged  int      `json:"lines_changed"`
	HasTestFiles  bool     `json:"has_test_files"`
	ResolvedIssue string   `json:"resolved_issue"`
	IssueCount    int      `json:"issue_count"`
	PassesFilter  bool     `json:"passes_filter"`
	FailReasons   []string `json:"fail_reasons"`
}

type FilterCriteria struct {
	MinFiles         int
	MinStars         int
	MinLines         int
	RequireTestFiles bool
	RequireSingleIssue bool
}

var defaultCriteria = FilterCriteria{
	MinFiles:           4,
	MinStars:           200,
	MinLines:           50,
	RequireTestFiles:   true,
	RequireSingleIssue: true,
}

func main() {
	// CLI flags
	showAll := flag.Bool("all", false, "Show all PRs including those that failed filters")
	inputFile := flag.String("input", "", "Input file containing PR URLs (default: stdin)")
	minFiles := flag.Int("min-files", 4, "Minimum number of files changed")
	minStars := flag.Int("min-stars", 200, "Minimum repository stars")
	minLines := flag.Int("min-lines", 50, "Minimum lines changed")
	requireTests := flag.Bool("require-tests", true, "Require test files to be changed")
	singleIssue := flag.Bool("single-issue", true, "Require exactly one issue to be resolved")
	verbose := flag.Bool("verbose", false, "Verbose output to stderr")
	outputFormat := flag.String("output", "table", "Output format: table or json")
	workers := flag.Int("workers", 10, "Number of concurrent workers")
	sortBy := flag.String("sort", "lines", "Sort by: lines, files, stars, repository")
	sortOrder := flag.String("sort-order", "desc", "Sort order: asc or desc")

	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: GITHUB_TOKEN environment variable is required")
		fmt.Fprintln(os.Stderr, "Set it with: export GITHUB_TOKEN=your_token_here")
		os.Exit(1)
	}

	// Build criteria from flags
	criteria := FilterCriteria{
		MinFiles:           *minFiles,
		MinStars:           *minStars,
		MinLines:           *minLines,
		RequireTestFiles:   *requireTests,
		RequireSingleIssue: *singleIssue,
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	var prURLs []string

	// Read input
	if *inputFile != "" {
		file, err := os.Open(*inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening input file: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				prURLs = append(prURLs, line)
			}
		}

		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}
	} else {
		scanner := bufio.NewScanner(os.Stdin)
		if !*verbose {
			fmt.Fprintln(os.Stderr, "Enter PR URLs (one per line, press Ctrl+D when done):")
		}

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				prURLs = append(prURLs, line)
			}
		}

		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			os.Exit(1)
		}
	}

	if len(prURLs) == 0 {
		fmt.Fprintln(os.Stderr, "No PR URLs provided")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n🔍 Processing %d PRs with %d workers...\n", len(prURLs), *workers)
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Process PRs concurrently
	results := processPRsConcurrently(ctx, client, prURLs, criteria, *workers, *verbose)

	// Filter results
	var toOutput []PRInfo
	if *showAll {
		toOutput = results
	} else {
		toOutput = make([]PRInfo, 0)
		for _, info := range results {
			if info.PassesFilter {
				toOutput = append(toOutput, info)
			}
		}
	}

	// Sort results
	sortResults(toOutput, *sortBy, *sortOrder)

	// Output results
	if *outputFormat == "json" {
		output, err := json.MarshalIndent(toOutput, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(output))
	} else {
		printTable(toOutput)
	}

	// Summary statistics
	passed := 0
	failureReasons := make(map[string]int)

	for _, info := range results {
		if info.PassesFilter {
			passed++
		} else {
			for _, reason := range info.FailReasons {
				failureReasons[reason]++
			}
		}
	}

	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(os.Stderr, "📊 Summary\n")
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	fmt.Fprintf(os.Stderr, "Total PRs processed:  %d\n", len(results))
	fmt.Fprintf(os.Stderr, "✅ Passed filters:    %d (%.1f%%)\n", passed, float64(passed)/float64(len(results))*100)
	fmt.Fprintf(os.Stderr, "❌ Failed filters:    %d (%.1f%%)\n\n", len(results)-passed, float64(len(results)-passed)/float64(len(results))*100)

	if len(failureReasons) > 0 {
		fmt.Fprintf(os.Stderr, "Top failure reasons:\n")
		type reasonCount struct {
			reason string
			count  int
		}
		var reasons []reasonCount
		for reason, count := range failureReasons {
			reasons = append(reasons, reasonCount{reason, count})
		}
		// Sort by count descending
		for i := 0; i < len(reasons); i++ {
			for j := i + 1; j < len(reasons); j++ {
				if reasons[j].count > reasons[i].count {
					reasons[i], reasons[j] = reasons[j], reasons[i]
				}
			}
		}
		for i, r := range reasons {
			if i >= 5 {
				break
			}
			fmt.Fprintf(os.Stderr, "  %d. %s (%d PRs)\n", i+1, r.reason, r.count)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}

	if *showAll {
		fmt.Fprintf(os.Stderr, "Output includes all PRs (passed and failed)\n")
	} else {
		fmt.Fprintf(os.Stderr, "Output includes only passing PRs (use -all to see failed PRs)\n")
	}
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

type prJob struct {
	index int
	url   string
}

type prResult struct {
	index int
	info  PRInfo
	err   error
}

func processPRsConcurrently(ctx context.Context, client *github.Client, prURLs []string, criteria FilterCriteria, workers int, verbose bool) []PRInfo {
	jobs := make(chan prJob, len(prURLs))
	results := make(chan prResult, len(prURLs))

	var wg sync.WaitGroup
	var logMutex sync.Mutex

	// Start workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				logMutex.Lock()
				fmt.Fprintf(os.Stderr, "[%d/%d] Fetching: %s\n", job.index+1, len(prURLs), job.url)
				logMutex.Unlock()

				info, err := processPR(ctx, client, job.url, criteria)

				logMutex.Lock()
				if err != nil {
					fmt.Fprintf(os.Stderr, "  ❌ Error: %v\n\n", err)
				} else {
					if info.PassesFilter {
						fmt.Fprintf(os.Stderr, "  ✅ PASSED - %s\n", info.Title)
						fmt.Fprintf(os.Stderr, "     Files: %d | Lines: %d | Stars: %d | Issue: %s\n",
							info.FilesChanged, info.LinesChanged, info.Stars, info.ResolvedIssue)
					} else {
						fmt.Fprintf(os.Stderr, "  ❌ FAILED - %s\n", info.Title)
						for _, reason := range info.FailReasons {
							fmt.Fprintf(os.Stderr, "     • %s\n", reason)
						}
					}

					if verbose {
						fmt.Fprintf(os.Stderr, "     Repo: %s | Files: %d | Lines: %d | Tests: %v | Stars: %d | Issues: %d\n",
							info.Repository, info.FilesChanged, info.LinesChanged, info.HasTestFiles, info.Stars, info.IssueCount)
					}

					fmt.Fprintf(os.Stderr, "\n")
				}
				logMutex.Unlock()

				results <- prResult{index: job.index, info: info, err: err}
			}
		}()
	}

	// Send jobs
	for i, url := range prURLs {
		jobs <- prJob{index: i, url: url}
	}
	close(jobs)

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and maintain original order
	resultMap := make(map[int]PRInfo)
	for result := range results {
		if result.err == nil {
			resultMap[result.index] = result.info
		}
	}

	// Build ordered results
	orderedResults := make([]PRInfo, 0, len(resultMap))
	for i := 0; i < len(prURLs); i++ {
		if info, ok := resultMap[i]; ok {
			orderedResults = append(orderedResults, info)
		}
	}

	return orderedResults
}

func processPR(ctx context.Context, client *github.Client, prURL string, criteria FilterCriteria) (PRInfo, error) {
	owner, repo, number, err := parsePRURL(prURL)
	if err != nil {
		return PRInfo{}, err
	}

	// Fetch PR details
	pr, _, err := client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return PRInfo{}, fmt.Errorf("failed to fetch PR: %w", err)
	}

	// Fetch repository details for star count
	repository, _, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return PRInfo{}, fmt.Errorf("failed to fetch repository: %w", err)
	}

	// Fetch files changed
	files, _, err := client.PullRequests.ListFiles(ctx, owner, repo, number, nil)
	if err != nil {
		return PRInfo{}, fmt.Errorf("failed to fetch files: %w", err)
	}

	// Calculate metrics
	filesChanged := len(files)
	linesChanged := pr.GetAdditions() + pr.GetDeletions()
	hasTestFiles := checkForTestFiles(files)

	// Extract resolved issues
	issues := extractIssues(pr.GetBody(), owner, repo)
	issueCount := len(issues)
	var resolvedIssue string
	if issueCount == 1 {
		resolvedIssue = issues[0]
	}

	// Apply filters
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
	}

	// Check criteria
	if filesChanged < criteria.MinFiles {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d files changed (need %d+)", filesChanged, criteria.MinFiles))
	}

	if !hasTestFiles && criteria.RequireTestFiles {
		info.FailReasons = append(info.FailReasons, "No test files changed")
	}

	if repository.GetStargazersCount() < criteria.MinStars {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d stars (need %d+)", repository.GetStargazersCount(), criteria.MinStars))
	}

	if linesChanged < criteria.MinLines {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d lines changed (need %d+)", linesChanged, criteria.MinLines))
	}

	if issueCount != 1 && criteria.RequireSingleIssue {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Resolves %d issues (need exactly 1)", issueCount))
	}

	info.PassesFilter = len(info.FailReasons) == 0

	return info, nil
}

func parsePRURL(url string) (owner, repo string, number int, err error) {
	// Match: https://github.com/owner/repo/pull/123
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

	// Match patterns like:
	// - fixes #123
	// - closes #456
	// - resolves https://github.com/owner/repo/issues/789
	// - (Closes #123)
	// - fix #123, #456 (but we want to count these separately)

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

func sortResults(prs []PRInfo, sortBy string, sortOrder string) {
	descending := sortOrder == "desc"

	sort.Slice(prs, func(i, j int) bool {
		var less bool

		switch sortBy {
		case "lines":
			less = prs[i].LinesChanged < prs[j].LinesChanged
		case "files":
			less = prs[i].FilesChanged < prs[j].FilesChanged
		case "stars":
			less = prs[i].Stars < prs[j].Stars
		case "repository":
			less = prs[i].Repository < prs[j].Repository
		default:
			// Default to lines if invalid sort field
			less = prs[i].LinesChanged < prs[j].LinesChanged
		}

		if descending {
			return !less
		}
		return less
	})
}

func printTable(prs []PRInfo) {
	if len(prs) == 0 {
		fmt.Println("No PRs to display")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Header
	fmt.Fprintln(w, "REPOSITORY\tSTARS\tFILES\tLINES\tRESOLVED ISSUE")

	// Rows
	for _, pr := range prs {
		issueURL := pr.ResolvedIssue
		if issueURL == "" {
			issueURL = "-"
		}

		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\n",
			pr.Repository,
			pr.Stars,
			pr.FilesChanged,
			pr.LinesChanged,
			issueURL,
		)
	}

	w.Flush()
}
