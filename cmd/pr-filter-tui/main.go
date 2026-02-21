package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
	"github.com/rdalbuquerque/pr-filter/tui"
)

const defaultAzureStorageAccount = "prfilterdata"
const defaultAzureContainer = "prdata"

func main() {
	dataPath := flag.String("data", "", "Path to local prs.json (if omitted, fetches from Azure Blob Storage)")
	evalPath := flag.String("eval", "", "Path to local ai-evaluations.json")
	statePath := flag.String("state", "", "Path to local-state.json (default: ~/.config/pr-filter/local-state.json)")
	configPath := flag.String("config", "", "Path to config file for filter/sort prefs")
	pageSize := flag.Int("page-size", 50, "Number of rows per page")
	sortBy := flag.String("sort", "lines", "Sort by: lines, files, stars, repository")
	sortOrder := flag.String("sort-order", "desc", "Sort order: asc or desc")
	flag.Parse()

	// Resolve state path
	localStatePath := *statePath
	if localStatePath == "" {
		localStatePath = tui.DefaultLocalStatePath()
	}

	var df *prdata.DataFile
	var aiEvals map[string]prdata.AIEvaluation
	var dataSource string
	var reloadSource *tui.ReloadSource
	loadOnStart := false

	if *dataPath != "" {
		// Local file mode
		dataSource = *dataPath
		var err error
		df, err = prdata.LoadDataFile(*dataPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading data file: %v\n", err)
			os.Exit(1)
		}

		// Load AI evaluations from local file
		resolvedEvalPath := *evalPath
		if resolvedEvalPath == "" {
			resolvedEvalPath = filepath.Join(filepath.Dir(*dataPath), "ai-evaluations.json")
		}
		ef, err := prdata.LoadAIEvaluationsFile(resolvedEvalPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load AI evaluations: %v\n", err)
		} else if ef != nil {
			aiEvals = ef.Evaluations
		}
	} else {
		// Azure Blob Storage mode — start TUI immediately and load data with progress
		account := envOrDefault("AZURE_STORAGE_ACCOUNT", defaultAzureStorageAccount)
		container := envOrDefault("AZURE_CONTAINER", defaultAzureContainer)
		dataSource = fmt.Sprintf("https://%s.blob.core.windows.net/%s", account, container)
		reloadSource = &tui.ReloadSource{Account: account, Container: container}
		df = &prdata.DataFile{Version: 1}
		loadOnStart = true
	}

	// Load local state
	localState, err := tui.LoadLocalState(localStatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load local state: %v\n", err)
		localState = &tui.LocalState{PRs: make(map[string]tui.PRLocalState)}
	}

	// Load config (filter/sort preferences)
	cfgPath := resolveConfigPath(*configPath)
	cfg := loadOrCreateConfig(cfgPath)

	// Merge data with local state and AI evaluations
	prs := mergePRsWithLocalState(df.PRs, localState, aiEvals)

	logs := []string{
		fmt.Sprintf("Loaded %d PRs from %s", len(df.PRs), dataSource),
		fmt.Sprintf("Data updated at: %s", df.UpdatedAt.Format(time.RFC3339)),
		fmt.Sprintf("Stats: %d total, %d pass1, %d pass2, %d taken",
			df.Stats.TotalFromSheet, df.Stats.HydratedPass1, df.Stats.HydratedPass2, df.Stats.TakenCount),
	}
	if len(aiEvals) > 0 {
		recommended := 0
		for _, ev := range aiEvals {
			if ev.Recommended {
				recommended++
			}
		}
		logs = append(logs, fmt.Sprintf("AI evaluations: %d total, %d recommended", len(aiEvals), recommended))
	}

	sortDesc := *sortOrder != "asc"
	if cfg.SortBy != "" {
		*sortBy = cfg.SortBy
	}
	if cfg.SortOrder != "" {
		sortDesc = cfg.SortOrder != "asc"
	}

	filters := cfg.Filters
	githubToken := os.Getenv("GITHUB_TOKEN")

	model := tui.NewModel(prs, tui.Options{
		PageSize:     *pageSize,
		SortBy:       *sortBy,
		SortDesc:     sortDesc,
		Logs:         logs,
		GitHubToken:  githubToken,
		Filters:      filters,
		ReloadSource: reloadSource,
		LoadOnStart:  loadOnStart,
		SaveFilters: func(f prdata.FilterState) {
			cfg.Filters = f
			writeConfig(cfgPath, cfg)
		},
		SavePR: func(pr tui.PRInfoView) {
			localState.PRs[pr.URL] = tui.PRLocalState{
				Checked: pr.Checked,
				Saved:   pr.Saved,
			}
			if err := tui.SaveLocalState(localStatePath, localState); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving local state: %v\n", err)
			}
		},
		DebugLog: setupDebugLog(),
	})

	p := tea.NewProgram(model, tea.WithFPS(30))

	// Start file watcher only in local mode
	if *dataPath != "" {
		resolvedEvalPath := *evalPath
		if resolvedEvalPath == "" {
			resolvedEvalPath = filepath.Join(filepath.Dir(*dataPath), "ai-evaluations.json")
		}
		go tui.WatchDataFiles(*dataPath, resolvedEvalPath, p)
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mergePRsWithLocalState(prs []prdata.PRInfo, state *tui.LocalState, evals map[string]prdata.AIEvaluation) []tui.PRInfoView {
	views := make([]tui.PRInfoView, 0, len(prs))
	for _, pr := range prs {
		view := tui.PRInfoView{PRInfo: pr}
		if ls, ok := state.PRs[pr.URL]; ok {
			view.Checked = ls.Checked
			view.Saved = ls.Saved
		}
		if evals != nil {
			if eval, ok := evals[pr.URL]; ok {
				view.AIRecommended = eval.Recommended
				view.AIScore = eval.Score
				view.AIReasoning = eval.Reasoning
				// Auto-favorite: AI recommended and user has no local state entry
				if eval.Recommended {
					if _, hasLocal := state.PRs[pr.URL]; !hasLocal {
						view.Saved = true
					}
				}
			}
		}
		views = append(views, view)
	}
	return views
}

// Config for filter/sort preferences
type config struct {
	SortBy    string             `json:"sort_by"`
	SortOrder string             `json:"sort_order"`
	Filters   prdata.FilterState `json:"filters"`
}

func resolveConfigPath(override string) string {
	if override != "" {
		return override
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(configDir, "pr-filter", "config.json")
}

func loadOrCreateConfig(path string) config {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{Filters: prdata.DefaultFilters()}
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{Filters: prdata.DefaultFilters()}
	}
	if cfg.Filters == (prdata.FilterState{}) {
		cfg.Filters = prdata.DefaultFilters()
	}
	return cfg
}

func writeConfig(path string, cfg config) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0600)
}

func setupDebugLog() func(string) {
	path := os.Getenv("PR_FILTER_LOG")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	return func(line string) {
		timestamp := time.Now().Format(time.RFC3339)
		fmt.Fprintf(file, "%s %s\n", timestamp, line)
	}
}
