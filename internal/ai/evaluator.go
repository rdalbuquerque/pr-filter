package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/rdalbuquerque/pr-filter/internal/prdata"
)

const (
	anthropicAPI     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"

	// Haiku 4.5 pricing per million tokens.
	inputPricePerMTok  = 1.00
	outputPricePerMTok = 5.00

	maxOutputTokens = 256
)

// EvaluatorConfig holds configuration for the AI evaluator.
type EvaluatorConfig struct {
	APIKey   string
	Model    string
	LimitUSD float64
}

// Evaluator calls the Anthropic API to evaluate PRs.
type Evaluator struct {
	cfg    EvaluatorConfig
	client *http.Client
}

// NewEvaluator creates an Evaluator with the given config.
func NewEvaluator(cfg EvaluatorConfig) *Evaluator {
	if cfg.Model == "" {
		cfg.Model = "claude-haiku-4-5-20251001"
	}
	return &Evaluator{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// EvaluatePR sends a single PR to the AI for evaluation.
// It returns the evaluation result and the actual token usage.
// The caller is responsible for cost-limit checks before calling.
func (e *Evaluator) EvaluatePR(ctx context.Context, pr prdata.PRInfo, issueBody string) (prdata.AIEvaluation, int, int, error) {
	prompt := buildPrompt(pr, issueBody)

	reqBody := apiRequest{
		Model:     e.cfg.Model,
		MaxTokens: maxOutputTokens,
		Messages: []apiMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPI, bytes.NewReader(bodyBytes))
	if err != nil {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := e.client.Do(req)
	if err != nil {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return prdata.AIEvaluation{}, 0, 0, fmt.Errorf("parse response: %w", err)
	}

	// Extract text from content blocks
	var text string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}

	eval, err := parseEvalResponse(text)
	if err != nil {
		return prdata.AIEvaluation{}, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens,
			fmt.Errorf("parse eval response: %w (raw: %s)", err, text)
	}

	eval.EvaluatedAt = time.Now()
	return eval, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, nil
}

// EstimateCost returns the estimated cost for a single evaluation.
func EstimateCost(inputTokens, outputTokens int) float64 {
	return float64(inputTokens)/1_000_000*inputPricePerMTok +
		float64(outputTokens)/1_000_000*outputPricePerMTok
}

// CostFromTokens computes cost from actual token counts.
func CostFromTokens(inputTokens, outputTokens int) float64 {
	return EstimateCost(inputTokens, outputTokens)
}

func buildPrompt(pr prdata.PRInfo, issueBody string) string {
	var sb strings.Builder
	sb.WriteString(`You evaluate GitHub PRs for suitability as AI coding training tasks. Be SELECTIVE — only recommend PRs that would genuinely challenge a capable AI coder.

A good training task:
- Requires real problem-solving: understanding context, designing a solution, implementing across multiple files
- Has at least 100+ lines of meaningful Python logic changes (not counting tests)
- Has a balanced mix of logic and test changes
- Is focused: addresses one specific issue, not a grab-bag of changes
- Primarily modifies Python source files (not dominated by docs, lock files, or configs)

Do NOT recommend if:
- The fix is trivial (small parameter change, one-liner, simple rename)
- Total lines changed is under 100 — too small to be a meaningful training task
- The change is mechanical (library swap, version bump, config tweak)
- It's mostly boilerplate or repetitive changes across files
- A competent developer could solve it in under 10 minutes

`)

	// Issue info
	sb.WriteString(fmt.Sprintf("Issue: %s\n", pr.Title))
	if issueBody != "" {
		body := issueBody
		if len(body) > 1500 {
			body = body[:1500] + "..."
		}
		sb.WriteString(body)
		sb.WriteString("\n\n")
	}

	// File breakdown
	var totalLines int
	for _, f := range pr.FileBreakdown {
		totalLines += f.Additions + f.Deletions
	}
	sb.WriteString(fmt.Sprintf("Files changed (%d files, %d lines):\n", len(pr.FileBreakdown), totalLines))
	for _, f := range pr.FileBreakdown {
		sb.WriteString(fmt.Sprintf("  %s: +%d -%d\n", f.Path, f.Additions, f.Deletions))
	}

	// Summary stats
	var pyLines, testLines, docLines, configLines int
	for _, f := range pr.FileBreakdown {
		lines := f.Additions + f.Deletions
		ext := strings.ToLower(filepath.Ext(f.Path))
		lower := strings.ToLower(f.Path)

		if isPythonFile(ext) {
			pyLines += lines
			if isTestFile(lower) {
				testLines += lines
			}
		}
		if isDocFile(ext) {
			docLines += lines
		}
		if isLockFile(lower) {
			configLines += lines
		}
	}

	sb.WriteString("\nSummary:\n")
	sb.WriteString(fmt.Sprintf("- Python source lines: %d (%.0f%%)\n", pyLines, pct(pyLines, totalLines)))
	sb.WriteString(fmt.Sprintf("- Test file lines: %d (%.0f%%)\n", testLines, pct(testLines, totalLines)))
	sb.WriteString(fmt.Sprintf("- Doc file lines (.md/.rst): %d (%.0f%%)\n", docLines, pct(docLines, totalLines)))
	sb.WriteString(fmt.Sprintf("- Config/lock file lines: %d (%.0f%%)\n", configLines, pct(configLines, totalLines)))

	sb.WriteString(`
Respond with JSON only:
{"recommended": bool, "score": 1-10, "reasoning": "1-2 sentences"}`)

	return sb.String()
}

func parseEvalResponse(text string) (prdata.AIEvaluation, error) {
	// Try to find JSON in the response
	text = strings.TrimSpace(text)

	// Strip markdown code fences if present
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				jsonLines = append(jsonLines, line)
			}
		}
		text = strings.Join(jsonLines, "\n")
	}

	// Find JSON object boundaries
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		text = text[start : end+1]
	}

	var result struct {
		Recommended bool   `json:"recommended"`
		Score       int    `json:"score"`
		Reasoning   string `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return prdata.AIEvaluation{}, fmt.Errorf("invalid JSON: %w", err)
	}

	if result.Score < 1 {
		result.Score = 1
	}
	if result.Score > 10 {
		result.Score = 10
	}

	return prdata.AIEvaluation{
		Recommended: result.Recommended,
		Score:       result.Score,
		Reasoning:   result.Reasoning,
	}, nil
}

// API request/response types

type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []apiContentBlock `json:"content"`
	Usage   apiUsage          `json:"usage"`
}

type apiContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
