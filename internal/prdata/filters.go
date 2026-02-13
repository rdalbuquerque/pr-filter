package prdata

import (
	"fmt"
	"strings"
)

// Matches returns true if the PR passes all filter criteria.
func (f FilterState) Matches(pr PRInfo) bool {
	if f.RepoQuery != "" {
		if !strings.Contains(strings.ToLower(pr.Repository), strings.ToLower(f.RepoQuery)) {
			return false
		}
	}
	if f.MinFiles > 0 && pr.FilesChanged < f.MinFiles {
		return false
	}
	if f.MinStars > 0 && (!pr.StarsKnown || pr.Stars < f.MinStars) {
		return false
	}
	if f.MinLines > 0 && pr.LinesChanged < f.MinLines {
		return false
	}
	if f.MaxLines > 0 && pr.LinesChanged > f.MaxLines {
		return false
	}
	if f.RequireTestFiles && (!pr.HasTestKnown || !pr.HasTestFiles) {
		return false
	}
	if f.RequireSingleIssue && pr.IssueCount != 1 {
		return false
	}
	return true
}

// ApplyDefaultFilters evaluates a PR against DefaultFilters and populates
// PassesFilter and FailReasons.
func ApplyDefaultFilters(info *PRInfo) {
	criteria := DefaultFilters()

	if info.FilesChanged < criteria.MinFiles {
		info.FailReasons = append(info.FailReasons, fmt.Sprintf("Only %d files changed (need %d+)", info.FilesChanged, criteria.MinFiles))
	}
	if criteria.RequireTestFiles && (!info.HasTestKnown || !info.HasTestFiles) {
		if !info.HasTestKnown {
			info.FailReasons = append(info.FailReasons, "Test file info not yet known")
		} else {
			info.FailReasons = append(info.FailReasons, "No test files changed")
		}
	}
	if !info.StarsKnown {
		info.FailReasons = append(info.FailReasons, "Star count not yet known")
	} else if info.Stars < criteria.MinStars {
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
