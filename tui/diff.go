package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func fetchDiffCmd(prURL, token string) tea.Cmd {
	return func() tea.Msg {
		content, err := fetchDiff(prURL, token)
		if err != nil {
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
	owner, repo, number, err := parsePRURL(prURL)
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

func renderDiff(content string) (string, error) {
	return highlightDiff(content)
}

type diffSection struct {
	file   string
	raw    string
	render string
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

func renderDiffSection(section diffSection) (string, error) {
	return highlightDiff(section.raw)
}

func highlightDiff(content string) (string, error) {
	lexer := lexers.Get("diff")
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	style = stripBackground(style)
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return content, nil
	}

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content, nil
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return content, nil
	}
	return buf.String(), nil
}

func stripBackground(style *chroma.Style) *chroma.Style {
	builder := chroma.NewStyleBuilder(style.Name + "-nobg")
	for _, ttype := range style.Types() {
		entry := style.Get(ttype)
		if entry.Background.IsSet() && !entry.Colour.IsSet() {
			entry.Colour = entry.Background
		}
		entry.Background = 0
		entry.Border = 0
		builder.AddEntry(ttype, entry)
	}

	builder.AddEntry(chroma.GenericInserted, chroma.StyleEntry{
		Colour:    chroma.MustParseColour("#5fd75f"),
		Bold:      chroma.Yes,
		NoInherit: true,
	})
	builder.AddEntry(chroma.GenericDeleted, chroma.StyleEntry{
		Colour:    chroma.MustParseColour("#ff5f5f"),
		Bold:      chroma.Yes,
		NoInherit: true,
	})
	newStyle, err := builder.Build()
	if err != nil {
		return style
	}
	return newStyle
}
