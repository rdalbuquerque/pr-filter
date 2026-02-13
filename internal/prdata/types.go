package prdata

import "time"

// PRInfo holds all metadata about a pull request.
// Checked/Saved are TUI-local state and not included here.
type PRInfo struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Number        int      `json:"number"`
	Repository    string   `json:"repository"`
	Stars         int      `json:"stars"`
	StarsKnown    bool     `json:"stars_known"`
	FilesChanged  int      `json:"files_changed"`
	LinesChanged  int      `json:"lines_changed"`
	HasTestFiles  bool     `json:"has_test_files"`
	HasTestKnown  bool     `json:"has_test_known"`
	Hydration      int    `json:"hydration"`
	HydrationError string `json:"hydration_error,omitempty"`
	HydrationRetries int  `json:"hydration_retries,omitempty"`
	ResolvedIssue string   `json:"resolved_issue"`
	IssueCount    int      `json:"issue_count"`
	PassesFilter  bool     `json:"passes_filter"`
	FailReasons   []string `json:"fail_reasons"`
	Taken         bool     `json:"taken"`
}

// FilterState holds user-configurable filter criteria.
type FilterState struct {
	RepoQuery          string `json:"repo_query"`
	MinFiles           int    `json:"min_files"`
	MinStars           int    `json:"min_stars"`
	MinLines           int    `json:"min_lines"`
	MaxLines           int    `json:"max_lines"`
	RequireTestFiles   bool   `json:"require_test_files"`
	RequireSingleIssue bool   `json:"require_single_issue"`
}

// DefaultFilters returns the standard filter configuration.
func DefaultFilters() FilterState {
	return FilterState{
		MinFiles:           4,
		MinStars:           200,
		MinLines:           50,
		RequireTestFiles:   true,
		RequireSingleIssue: true,
	}
}

// SheetPRRow represents a single row from the Google Sheet.
type SheetPRRow struct {
	Repo         string `json:"repo"`
	PRLink       string `json:"pr_link"`
	FilesChanged int    `json:"files_changed"`
	LinesChanged int    `json:"lines_changed"`
	Taken        bool   `json:"taken"`
}

// DataFile is the intermediate JSON format written by the fetcher and read by the TUI.
type DataFile struct {
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
	SheetPollAt time.Time `json:"sheet_poll_at"`
	PRs         []PRInfo  `json:"prs"`
	Stats       DataStats `json:"stats"`
}

// DataStats tracks progress of the data pipeline.
type DataStats struct {
	TotalFromSheet int `json:"total_from_sheet"`
	HydratedPass1  int `json:"hydrated_pass1"`
	HydratedPass2  int `json:"hydrated_pass2"`
	TakenCount     int `json:"taken_count"`
}
