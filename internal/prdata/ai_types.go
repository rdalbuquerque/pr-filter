package prdata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AIEvaluation holds the result of evaluating a single PR.
type AIEvaluation struct {
	Recommended   bool      `json:"recommended"`
	Score         int       `json:"score"`
	Reasoning     string    `json:"reasoning"`
	EvaluatedAt   time.Time `json:"evaluated_at"`
	HeuristicOnly bool      `json:"heuristic_only"`
}

// AIEvaluationsFile is the top-level structure persisted to ai-evaluations.json.
type AIEvaluationsFile struct {
	Version      int                     `json:"version"`
	UpdatedAt    time.Time               `json:"updated_at"`
	Evaluations  map[string]AIEvaluation `json:"evaluations"` // keyed by PR URL
	CostTracking CostTracking            `json:"cost_tracking"`
}

// CostTracking tracks cumulative API usage and enforces a spending limit.
type CostTracking struct {
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	LimitUSD          float64 `json:"limit_usd"`
}

// NewAIEvaluationsFile creates an empty evaluations file with the given cost limit.
func NewAIEvaluationsFile(limitUSD float64) *AIEvaluationsFile {
	return &AIEvaluationsFile{
		Version:     1,
		UpdatedAt:   time.Now(),
		Evaluations: make(map[string]AIEvaluation),
		CostTracking: CostTracking{
			LimitUSD: limitUSD,
		},
	}
}

// LoadAIEvaluationsFile reads and parses ai-evaluations.json.
// Returns nil, nil if the file does not exist.
func LoadAIEvaluationsFile(path string) (*AIEvaluationsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ai evaluations file: %w", err)
	}

	var ef AIEvaluationsFile
	if err := json.Unmarshal(data, &ef); err != nil {
		return nil, fmt.Errorf("parse ai evaluations file: %w", err)
	}
	if ef.Evaluations == nil {
		ef.Evaluations = make(map[string]AIEvaluation)
	}
	return &ef, nil
}

// SaveAIEvaluationsFile atomically writes ai-evaluations.json.
func SaveAIEvaluationsFile(path string, ef *AIEvaluationsFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create eval dir: %w", err)
	}

	ef.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(ef, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ai evaluations file: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
