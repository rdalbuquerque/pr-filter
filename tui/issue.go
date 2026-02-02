package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"
)

type issueMsg struct {
	content string
	err     error
}

func fetchIssueCmd(issueURL, token string) tea.Cmd {
	return func() tea.Msg {
		content, err := fetchIssue(issueURL, token)
		if err != nil {
			return issueMsg{err: err}
		}
		rendered, err := renderIssue(content)
		if err != nil {
			return issueMsg{err: err}
		}
		return issueMsg{content: rendered}
	}
}

func fetchIssue(issueURL, token string) (string, error) {
	owner, repo, number, err := parseIssueURL(issueURL)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", owner, repo, number)
	client := &http.Client{Timeout: 30 * time.Second}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github.v3+json")
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
		return "", fmt.Errorf("issue request failed: %s %s", resp.Status, string(body))
	}

	var payload struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}

	content := fmt.Sprintf("# %s\n\n%s", payload.Title, payload.Body)
	return content, nil
}

func renderIssue(content string) (string, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(100),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return "", err
	}
	return renderer.Render(content)
}

func parseIssueURL(url string) (owner, repo string, number int, err error) {
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid issue URL: %s", url)
	}

	owner = matches[1]
	repo = matches[2]
	fmt.Sscanf(matches[3], "%d", &number)
	return owner, repo, number, nil
}
