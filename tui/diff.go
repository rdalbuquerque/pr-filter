package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	ghpkg "github.com/revelo/pr-filter/internal/github"
)

func fetchDiffCmd(prURL, token string) tea.Cmd {
	return func() tea.Msg {
		content, err := fetchDiff(prURL, token)
		if err != nil {
			if tooLarge := isDiffTooLarge(err); tooLarge {
				files, fileErr := fetchDiffFiles(prURL, token)
				if fileErr != nil {
					return diffMsg{err: err}
				}
				return diffMsg{files: files}
			}
			return diffMsg{err: err}
		}
		highlighted, err := renderDiff(content)
		if err != nil {
			return diffMsg{err: err}
		}
		return diffMsg{raw: content, content: highlighted}
	}
}

func fetchDiff(prURL, token string) (string, error) {
	owner, repo, number, err := ghpkg.ParsePRURL(prURL)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number)
	client := &http.Client{Timeout: 30 * time.Second}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github.v3.diff")
	request.Header.Set("User-Agent", "pr-filter-tui")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("diff request failed: %s %s", resp.Status, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isDiffTooLarge(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "diff exceeded the maximum number of files") || strings.Contains(err.Error(), "too_large")
}

func fetchDiffFiles(prURL, token string) ([]string, error) {
	owner, repo, number, err := ghpkg.ParsePRURL(prURL)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	files := make([]string, 0)
	page := 1
	for {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/files?per_page=100&page=%d", owner, repo, number, page)
		request, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/vnd.github.v3+json")
		request.Header.Set("User-Agent", "pr-filter-tui")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		var payload []struct {
			Filename string `json:"filename"`
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("list files failed: %s %s", resp.Status, string(body))
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if len(payload) == 0 {
			break
		}
		for _, item := range payload {
			if item.Filename != "" {
				files = append(files, item.Filename)
			}
		}
		page++
	}

	return files, nil
}

func renderDiff(content string) (string, error) {
	return renderInlineDiff(content), nil
}

type diffSection struct {
	file       string
	raw        string
	render     string
	renderSide string
}

func parseDiffSections(content string) []diffSection {
	lines := strings.Split(content, "\n")
	sections := make([]diffSection, 0)
	var current *diffSection
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			if current != nil {
				sections = append(sections, *current)
			}
			file := extractDiffFile(line)
			current = &diffSection{file: file, raw: line + "\n"}
			continue
		}
		if current == nil {
			continue
		}
		current.raw += line + "\n"
	}
	if current != nil {
		sections = append(sections, *current)
	}
	if len(sections) == 0 && content != "" {
		sections = append(sections, diffSection{file: "diff", raw: content})
	}
	return sections
}

func extractDiffFile(line string) string {
	parts := strings.Split(line, " ")
	if len(parts) < 4 {
		return ""
	}
	path := parts[3]
	path = strings.TrimPrefix(path, "b/")
	return path
}

type sideBySideLine struct {
	left      string
	right     string
	leftType  string
	rightType string
}

func renderSideBySideDiff(raw string, width int) string {
	if width < 10 {
		width = 10
	}
	sep := " │ "
	leftWidth := (width - len(sep)) / 2
	rightWidth := width - len(sep) - leftWidth
	lines := buildSideBySideLines(raw)
	if len(lines) == 0 {
		return raw
	}

	var out bytes.Buffer
	for i, line := range lines {
		left := formatSideCell(line.left, leftWidth)
		right := formatSideCell(line.right, rightWidth)
		left = styleSideCell(left, line.leftType)
		right = styleSideCell(right, line.rightType)
		out.WriteString(left)
		out.WriteString(sep)
		out.WriteString(right)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

func buildSideBySideLines(raw string) []sideBySideLine {
	lines := strings.Split(raw, "\n")
	output := make([]sideBySideLine, 0, len(lines))
	var dels []string
	var adds []string
	flush := func() {
		max := len(dels)
		if len(adds) > max {
			max = len(adds)
		}
		for i := 0; i < max; i++ {
			left := ""
			right := ""
			if i < len(dels) {
				left = dels[i]
			}
			if i < len(adds) {
				right = adds[i]
			}
			output = append(output, sideBySideLine{left: left, right: right, leftType: lineType(left), rightType: lineType(right)})
		}
		dels = nil
		adds = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			flush()
			output = append(output, sideBySideLine{left: line, right: "", leftType: "header"})
			continue
		}
		if strings.HasPrefix(line, "@@") {
			flush()
			output = append(output, sideBySideLine{left: line, right: line, leftType: "hunk", rightType: "hunk"})
			continue
		}
		if strings.HasPrefix(line, "-") {
			dels = append(dels, line)
			continue
		}
		if strings.HasPrefix(line, "+") {
			adds = append(adds, line)
			continue
		}
		if strings.HasPrefix(line, " ") || line == "" {
			flush()
			output = append(output, sideBySideLine{left: line, right: line, leftType: lineType(line), rightType: lineType(line)})
			continue
		}
	}
	flush()
	return output
}

func lineType(line string) string {
	if line == "" {
		return "context"
	}
	switch line[0] {
	case '+':
		return "add"
	case '-':
		return "del"
	case '@':
		return "hunk"
	default:
		return "context"
	}
}

func formatSideCell(value string, width int) string {
	if width <= 0 {
		return ""
	}
	trimmed := runewidth.Truncate(value, width, "")
	return runewidth.FillRight(trimmed, width)
}

func styleSideCell(value string, kind string) string {
	switch kind {
	case "add":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("70")).Render(value)
	case "del":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("160")).Render(value)
	case "hunk":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Render(value)
	case "header":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(value)
	default:
		return value
	}
}

func renderDiffSection(section diffSection) (string, error) {
	return renderInlineDiff(section.raw), nil
}

func renderInlineDiff(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, styleSideCell(line, inlineLineType(line)))
	}
	return strings.Join(out, "\n")
}

func inlineLineType(line string) string {
	if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
		return "header"
	}
	if strings.HasPrefix(line, "@@") {
		return "hunk"
	}
	return lineType(line)
}
