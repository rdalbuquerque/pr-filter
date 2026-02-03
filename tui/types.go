package tui

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
	Taken         bool     `json:"taken"`
	Checked       bool     `json:"checked"`
	Saved         bool     `json:"saved"`
}

type FilterState struct {
	RepoQuery          string
	MinFiles           int
	MinStars           int
	MinLines           int
	MaxLines           int
	RequireTestFiles   bool
	RequireSingleIssue bool
}

func DefaultFilters() FilterState {
	return FilterState{
		MinFiles:           4,
		MinStars:           200,
		MinLines:           50,
		RequireTestFiles:   true,
		RequireSingleIssue: true,
	}
}
