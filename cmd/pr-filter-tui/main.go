package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/revelo/pr-filter/tui"
)

func main() {
	configPath := flag.String("config", "", "Path to config file (default: ~/.config/pr-filter/config.json)")
	dataPath := flag.String("data", "", "Path to JSON file with PR data")
	cachePath := flag.String("cache", "", "Path to BoltDB cache file")
	sheetID := flag.String("sheet-id", "", "Google Sheet ID")
	sheetGID := flag.Int64("sheet-gid", 0, "Sheet tab GID")
	sheetName := flag.String("sheet-name", "", "Sheet name (defaults to gid or first sheet)")
	sheetRange := flag.String("sheet-range", "", "Sheet range (e.g. Sheet1!A:Z)")
	googleSecret := flag.String("google-secret", "", "Google OAuth client secret JSON")
	googleToken := flag.String("google-token", "", "Path to Google OAuth token cache")
	refresh := flag.Bool("refresh", false, "Refresh cache from Google Sheets")
	workers := flag.Int("workers", 10, "Number of concurrent GitHub workers")
	pageSize := flag.Int("page-size", 50, "Number of rows per page")
	sortBy := flag.String("sort", "lines", "Sort by: lines, files, stars, repository")
	sortOrder := flag.String("sort-order", "desc", "Sort order: asc or desc")
	flag.Parse()

	cfgPath := resolveConfigPath(*configPath)
	cfg, cfgErr := loadOrCreateConfig(cfgPath)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", cfgErr)
		os.Exit(1)
	}

	applyOverrides(&cfg, overrides{
		dataPath:     *dataPath,
		cachePath:    *cachePath,
		sheetID:      *sheetID,
		sheetGID:     *sheetGID,
		sheetName:    *sheetName,
		sheetRange:   *sheetRange,
		googleSecret: *googleSecret,
		googleToken:  *googleToken,
		refresh:      *refresh,
		workers:      *workers,
		pageSize:     *pageSize,
		sortBy:       *sortBy,
		sortOrder:    *sortOrder,
	})

	normalizeConfig(&cfg)

	prs, logs, err := loadData(cfg.CachePath, cfg.DataPath, cfg.Refresh)
	if err != nil {
		if errors.Is(err, errCacheEmpty) || errors.Is(err, errCacheMissing) {
			prs, logs, err = fetchAndCache(cfg, logs)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading data: %v\n", err)
		os.Exit(1)
	}

	sortDesc := cfg.SortOrder != "asc"
	model := tui.NewModel(prs, tui.Options{
		PageSize:    cfg.PageSize,
		SortBy:      cfg.SortBy,
		SortDesc:    sortDesc,
		Logs:        logs,
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		Filters:     cfg.Filters,
		SaveFilters: func(filters tui.FilterState) {
			cfg.Filters = filters
			if err := writeConfig(cfgPath, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			}
		},
		SavePR: func(pr tui.PRInfo) {
			if cfg.CachePath == "" {
				return
			}
			if err := tui.SavePRToBolt(cfg.CachePath, pr); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving PR: %v\n", err)
			}
		},
		DebugLog: setupDebugLog(),
	})

	if _, err := tea.NewProgram(model, tea.WithFPS(30)).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var errCacheEmpty = errors.New("cache empty")
var errCacheMissing = errors.New("cache missing")

func loadData(cachePath, dataPath string, refresh bool) ([]tui.PRInfo, []string, error) {
	logs := make([]string, 0)
	if dataPath != "" {
		prs, err := tui.LoadPRsFromJSON(dataPath)
		if err != nil {
			return nil, logs, err
		}
		logs = append(logs, fmt.Sprintf("Loaded %d PRs from %s", len(prs), dataPath))
		return prs, logs, nil
	}
	if cachePath == "" {
		return nil, logs, fmt.Errorf("cache path is required")
	}
	if refresh {
		return nil, logs, errCacheEmpty
	}
	if _, err := os.Stat(cachePath); err != nil {
		if os.IsNotExist(err) {
			return nil, logs, errCacheMissing
		}
		return nil, logs, err
	}
	prs, err := tui.LoadPRsFromBolt(cachePath)
	if err != nil {
		return nil, logs, err
	}
	if len(prs) == 0 {
		return nil, logs, errCacheEmpty
	}
	logs = append(logs, fmt.Sprintf("Loaded %d PRs from cache", len(prs)))
	return prs, logs, nil
}

type overrides struct {
	dataPath     string
	cachePath    string
	sheetID      string
	sheetGID     int64
	sheetName    string
	sheetRange   string
	googleSecret string
	googleToken  string
	refresh      bool
	workers      int
	pageSize     int
	sortBy       string
	sortOrder    string
}

type config struct {
	DataPath     string          `json:"data_path"`
	CachePath    string          `json:"cache_path"`
	SheetID      string          `json:"sheet_id"`
	SheetGID     int64           `json:"sheet_gid"`
	SheetName    string          `json:"sheet_name"`
	SheetRange   string          `json:"sheet_range"`
	GoogleSecret string          `json:"google_secret"`
	GoogleToken  string          `json:"google_token"`
	Refresh      bool            `json:"refresh"`
	Workers      int             `json:"workers"`
	PageSize     int             `json:"page_size"`
	SortBy       string          `json:"sort_by"`
	SortOrder    string          `json:"sort_order"`
	Filters      tui.FilterState `json:"filters"`
}

func fetchAndCache(cfg config, logs []string) ([]tui.PRInfo, []string, error) {
	if cfg.SheetID == "" {
		return nil, logs, fmt.Errorf("sheet-id is required when cache is empty")
	}
	if cfg.GoogleSecret == "" {
		return nil, logs, fmt.Errorf("google-secret is required when fetching from sheets")
	}

	googleToken := cfg.GoogleToken
	if googleToken == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, logs, fmt.Errorf("user config dir: %w", err)
		}
		googleToken = filepath.Join(configDir, "pr-filter", "token.json")
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		return nil, logs, fmt.Errorf("GITHUB_TOKEN is required for GitHub fetch")
	}

	ctx := context.Background()
	links, err := tui.LoadPRLinksFromSheet(ctx, tui.SheetOptions{
		SheetID:    cfg.SheetID,
		SheetGID:   cfg.SheetGID,
		SheetName:  cfg.SheetName,
		SheetRange: cfg.SheetRange,
		CredsPath:  cfg.GoogleSecret,
		TokenPath:  googleToken,
	})
	if err != nil {
		return nil, logs, err
	}
	if len(links) == 0 {
		return nil, logs, fmt.Errorf("no PR links found in sheet")
	}

	cacheEntries := make(map[string]tui.PRInfo)
	if cfg.CachePath != "" {
		if cached, err := tui.LoadPRsFromBolt(cfg.CachePath); err == nil {
			for _, pr := range cached {
				if pr.URL == "" {
					continue
				}
				cacheEntries[pr.URL] = pr
			}
		}
	}

	missing := make([]string, 0, len(links))
	for _, link := range links {
		if _, ok := cacheEntries[link]; ok {
			continue
		}
		missing = append(missing, link)
	}

	if len(missing) == 0 {
		prs := make([]tui.PRInfo, 0, len(cacheEntries))
		for _, pr := range cacheEntries {
			prs = append(prs, pr)
		}
		logs = append(logs, fmt.Sprintf("All %d PRs loaded from cache", len(prs)))
		return prs, logs, nil
	}

	logs = append(logs, fmt.Sprintf("Fetching %d PRs...", len(missing)))
	prs, fetchErrors := tui.FetchPRs(ctx, missing, githubToken, tui.FetchOptions{Workers: cfg.Workers})
	if len(fetchErrors) > 0 {
		logs = append(logs, fmt.Sprintf("Warning: %d PRs failed to fetch (first error: %v)", len(fetchErrors), fetchErrors[0]))
	}

	if len(prs) == 0 && len(cacheEntries) > 0 {
		fallback := make([]tui.PRInfo, 0, len(cacheEntries))
		for _, pr := range cacheEntries {
			fallback = append(fallback, pr)
		}
		logs = append(logs, "Fetch returned 0 PRs, using cached data")
		return fallback, logs, nil
	}

	merged := make([]tui.PRInfo, 0, len(cacheEntries)+len(prs))
	for _, pr := range cacheEntries {
		merged = append(merged, pr)
	}
	for _, pr := range prs {
		if cached, ok := cacheEntries[pr.URL]; ok {
			pr.Checked = cached.Checked
			pr.Saved = cached.Saved
		}
		merged = append(merged, pr)
	}

	if cfg.CachePath != "" {
		if err := tui.SavePRsToBolt(cfg.CachePath, merged); err != nil {
			return nil, logs, err
		}
	}

	logs = append(logs, fmt.Sprintf("Loaded %d PRs (cached: %d, fetched: %d)", len(merged), len(cacheEntries), len(prs)))
	return merged, logs, nil
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

func loadOrCreateConfig(path string) (config, error) {
	if _, err := os.Stat(path); err == nil {
		return loadConfig(path)
	}

	defaultCfg, err := defaultConfig()
	if err != nil {
		return config{}, err
	}
	if err := writeConfig(path, defaultCfg); err != nil {
		return config{}, err
	}
	return defaultCfg, nil
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func writeConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func defaultConfig() (config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return config{}, err
	}
	cachePath := filepath.Join(configDir, "pr-filter", "cache.db")
	return config{
		CachePath:    cachePath,
		SheetID:      "1WnGf8ULFHVpTjnpLz46DH-UrvOCLtkDmqilZLzaS4KM",
		SheetGID:     886975217,
		GoogleSecret: "client_secret_1047690774768-7u0fn9fkn61g2nhu1kcrsj0otdoobjtg.apps.googleusercontent.com.json",
		GoogleToken:  filepath.Join(configDir, "pr-filter", "token.json"),
		Workers:      10,
		PageSize:     50,
		SortBy:       "lines",
		SortOrder:    "desc",
		Filters:      tui.DefaultFilters(),
	}, nil
}

func normalizeConfig(cfg *config) {
	if cfg.Filters == (tui.FilterState{}) {
		cfg.Filters = tui.DefaultFilters()
	}
}

func setupDebugLog() func(string) {
	path := os.Getenv("PR_FILTER_LOG")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening PR_FILTER_LOG: %v\n", err)
		return nil
	}

	return func(line string) {
		timestamp := time.Now().Format(time.RFC3339)
		fmt.Fprintf(file, "%s %s\n", timestamp, line)
	}
}

func applyOverrides(cfg *config, o overrides) {
	if o.dataPath != "" {
		cfg.DataPath = o.dataPath
	}
	if o.cachePath != "" {
		cfg.CachePath = o.cachePath
	}
	if o.sheetID != "" {
		cfg.SheetID = o.sheetID
	}
	if o.sheetGID != 0 {
		cfg.SheetGID = o.sheetGID
	}
	if o.sheetName != "" {
		cfg.SheetName = o.sheetName
	}
	if o.sheetRange != "" {
		cfg.SheetRange = o.sheetRange
	}
	if o.googleSecret != "" {
		cfg.GoogleSecret = o.googleSecret
	}
	if o.googleToken != "" {
		cfg.GoogleToken = o.googleToken
	}
	if o.refresh {
		cfg.Refresh = true
	}
	if o.workers > 0 {
		cfg.Workers = o.workers
	}
	if o.pageSize > 0 {
		cfg.PageSize = o.pageSize
	}
	if o.sortBy != "" {
		cfg.SortBy = o.sortBy
	}
	if o.sortOrder != "" {
		cfg.SortOrder = o.sortOrder
	}
}
