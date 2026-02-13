package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LocalState stores per-PR user state (checked/saved) separate from the data file.
type LocalState struct {
	PRs map[string]PRLocalState `json:"prs"`
}

// PRLocalState holds user-toggled state for a single PR.
type PRLocalState struct {
	Checked bool `json:"checked"`
	Saved   bool `json:"saved"`
}

// LoadLocalState reads local state from a JSON file.
// Returns an empty state if the file doesn't exist.
func LoadLocalState(path string) (*LocalState, error) {
	state := &LocalState{PRs: make(map[string]PRLocalState)}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("read local state: %w", err)
	}

	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parse local state: %w", err)
	}

	if state.PRs == nil {
		state.PRs = make(map[string]PRLocalState)
	}

	return state, nil
}

// SaveLocalState writes local state to a JSON file.
func SaveLocalState(path string, state *LocalState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal local state: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// DefaultLocalStatePath returns the default path for local state storage.
func DefaultLocalStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "local-state.json"
	}
	return filepath.Join(configDir, "pr-filter", "local-state.json")
}
