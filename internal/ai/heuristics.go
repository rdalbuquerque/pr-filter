package ai

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/revelo/pr-filter/internal/prdata"
)

// HeuristicResult holds the outcome of the heuristic pre-filter.
type HeuristicResult struct {
	Pass   bool
	Reason string // non-empty when Pass is false
}

// RunHeuristics applies fast code-based checks to eliminate obvious non-candidates
// before calling the AI. Returns a passing result if the PR survives all checks.
func RunHeuristics(files []prdata.FileDetail) HeuristicResult {
	if len(files) == 0 {
		return HeuristicResult{Pass: false, Reason: "no file breakdown available"}
	}

	var (
		totalLines  int
		pyLines     int
		lockLines   int
		docLines    int
		testLines   int
		renameCount int
	)

	for _, f := range files {
		lines := f.Additions + f.Deletions
		totalLines += lines

		lower := strings.ToLower(f.Path)
		ext := strings.ToLower(filepath.Ext(f.Path))

		if isPythonFile(ext) {
			if isTestFile(lower) {
				testLines += lines
			}
			pyLines += lines
		}

		if isLockFile(lower) {
			lockLines += lines
		}
		if isDocFile(ext) {
			docLines += lines
		}
		if isRenamePattern(f) {
			renameCount++
		}
	}

	if totalLines == 0 {
		return HeuristicResult{Pass: false, Reason: "zero lines changed"}
	}

	// 1. Python ratio: ≥60% of lines must be in .py files
	pyPct := pct(pyLines, totalLines)
	if pyPct < 60 {
		return HeuristicResult{
			Pass:   false,
			Reason: fmt.Sprintf("only %.0f%% Python lines (need ≥60%%)", pyPct),
		}
	}

	// 2. Lock/config ratio: ≤20% of lines in lock/generated files
	lockPct := pct(lockLines, totalLines)
	if lockPct > 20 {
		return HeuristicResult{
			Pass:   false,
			Reason: fmt.Sprintf("%.0f%% lock/generated file lines (max 20%%)", lockPct),
		}
	}

	// 3. Doc file ratio: ≤30% of lines in doc files
	docPct := pct(docLines, totalLines)
	if docPct > 30 {
		return HeuristicResult{
			Pass:   false,
			Reason: fmt.Sprintf("%.0f%% doc file lines (max 30%%)", docPct),
		}
	}

	// 4. Test balance: test file lines should be 10-70% of total
	testPct := pct(testLines, totalLines)
	if testPct < 10 {
		return HeuristicResult{
			Pass:   false,
			Reason: fmt.Sprintf("only %.0f%% test lines (need 10-70%%)", testPct),
		}
	}
	if testPct > 70 {
		return HeuristicResult{
			Pass:   false,
			Reason: fmt.Sprintf("%.0f%% test lines (max 70%%, too test-heavy)", testPct),
		}
	}

	// 5. Not all-rename: if >90% of files have additions ≈ deletions (±5 lines), likely a rename
	if len(files) > 0 {
		renamePct := pct(renameCount, len(files))
		if renamePct > 90 {
			return HeuristicResult{
				Pass:   false,
				Reason: fmt.Sprintf("%.0f%% of files look like renames/moves", renamePct),
			}
		}
	}

	return HeuristicResult{Pass: true}
}

func isPythonFile(ext string) bool {
	return ext == ".py" || ext == ".pyx" || ext == ".pyi"
}

func isTestFile(lower string) bool {
	return strings.Contains(lower, "test") || strings.Contains(lower, "spec")
}

func isLockFile(lower string) bool {
	base := filepath.Base(lower)
	lockFiles := []string{
		"poetry.lock", "uv.lock", "package-lock.json", "yarn.lock",
		"pipfile.lock", "pnpm-lock.yaml", "cargo.lock", "go.sum",
	}
	if slices.Contains(lockFiles, base) {
		return true
	}
	return strings.HasSuffix(lower, ".lock")
}

func isDocFile(ext string) bool {
	return ext == ".md" || ext == ".rst" || ext == ".txt"
}

func isRenamePattern(f prdata.FileDetail) bool {
	return math.Abs(float64(f.Additions-f.Deletions)) <= 5
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
