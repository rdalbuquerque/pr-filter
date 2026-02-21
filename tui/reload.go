package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
)

// reloadProgressMsg is sent during download to update the progress bar.
type reloadProgressMsg struct {
	percent float64
	label   string
}

// reloadDoneMsg is sent when the reload completes (or fails).
type reloadDoneMsg struct {
	prs   []prdata.PRInfo
	evals map[string]prdata.AIEvaluation
	err   error
}

// waitForProgress returns a command that waits for the next progress message
// from the channel and converts it to a tea.Msg.
func waitForProgress(ch <-chan reloadProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// waitForDone returns a command that waits for the download to finish.
func waitForDone(ch <-chan reloadDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (m *Model) startReload() tea.Cmd {
	m.reloading = true
	m.reloadProgress = 0
	m.reloadLabel = "Starting..."

	src := m.reloadSource
	progressCh := make(chan reloadProgressMsg, 64)
	doneCh := make(chan reloadDoneMsg, 1)
	m.reloadCh = progressCh

	// Start download in background goroutine
	go func() {
		defer close(progressCh)
		defer close(doneCh)

		baseURL := fmt.Sprintf("https://%s.blob.core.windows.net/%s", src.Account, src.Container)

		// Download prs.json with progress (0-0.8)
		prsData, err := downloadWithProgress(baseURL+"/prs.json", "prs.json", func(percent float64, label string) {
			progressCh <- reloadProgressMsg{percent: percent * 0.8, label: label}
		})
		if err != nil {
			doneCh <- reloadDoneMsg{err: fmt.Errorf("fetch prs.json: %w", err)}
			return
		}

		var df prdata.DataFile
		if err := json.Unmarshal(prsData, &df); err != nil {
			doneCh <- reloadDoneMsg{err: fmt.Errorf("parse prs.json: %w", err)}
			return
		}

		// Download ai-evaluations.json (0.8-1.0)
		progressCh <- reloadProgressMsg{percent: 0.82, label: "ai-evaluations.json"}
		var evals map[string]prdata.AIEvaluation
		evalData, err := downloadWithProgress(baseURL+"/ai-evaluations.json", "ai-evaluations.json", func(percent float64, label string) {
			progressCh <- reloadProgressMsg{percent: 0.8 + percent*0.2, label: label}
		})
		if err == nil {
			var ef prdata.AIEvaluationsFile
			if err := json.Unmarshal(evalData, &ef); err == nil {
				evals = ef.Evaluations
			}
		}

		progressCh <- reloadProgressMsg{percent: 1.0, label: "Done"}

		doneCh <- reloadDoneMsg{prs: df.PRs, evals: evals}
	}()

	// Return commands that listen on both channels
	return tea.Batch(
		waitForProgress(progressCh),
		waitForDone(doneCh),
	)
}

// progressReader wraps an io.Reader and reports progress.
type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	fileName   string
	onProgress func(float64, string)
	lastReport time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.downloaded += int64(n)

	// Throttle progress updates to ~20fps
	if time.Since(pr.lastReport) > 50*time.Millisecond || err == io.EOF {
		pr.lastReport = time.Now()
		percent := 0.0
		if pr.total > 0 {
			percent = float64(pr.downloaded) / float64(pr.total)
			if percent > 1.0 {
				percent = 1.0
			}
		}
		label := fmt.Sprintf("%s (%s / %s)",
			pr.fileName,
			formatBytes(pr.downloaded),
			formatBytes(pr.total))
		pr.onProgress(percent, label)
	}

	return n, err
}

func downloadWithProgress(url, fileName string, onProgress func(float64, string)) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	pr := &progressReader{
		reader:     resp.Body,
		total:      resp.ContentLength,
		fileName:   fileName,
		onProgress: onProgress,
	}

	return io.ReadAll(pr)
}

func formatBytes(b int64) string {
	if b < 0 {
		return "?"
	}
	const mb = 1024 * 1024
	const kb = 1024
	switch {
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func (m Model) viewReloading() string {
	title := lipgloss.NewStyle().Bold(true).Render("Reloading data from Azure...")
	bar := m.progressBar.ViewAs(m.reloadProgress)
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.reloadLabel)

	lines := []string{
		title,
		"",
		bar,
		label,
	}
	return strings.Join(lines, "\n")
}
