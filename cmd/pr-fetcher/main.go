package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	ghpkg "github.com/rdalbuquerque/pr-filter/internal/github"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
	"github.com/rdalbuquerque/pr-filter/internal/storage"
)

type serviceConfig struct {
	OutputPath        string
	SheetID           string
	SheetGID          int64
	GoogleSecret      string
	GoogleToken       string
	GitHubToken       string
	SheetPollInterval time.Duration
	HydrationInterval time.Duration
	Workers           int
	HydrationBatch    int
	// Azure Blob Storage
	AzureStorageAccount string
	AzureStorageKey     string
	AzureContainer      string
}

func main() {
	// Check for --setup flag
	if len(os.Args) > 1 && os.Args[1] == "--setup" {
		runSetup()
		return
	}

	cfg := loadEnvConfig()
	log.Printf("pr-fetcher starting")
	log.Printf("  output: %s", cfg.OutputPath)
	log.Printf("  sheet: %s (gid %d)", cfg.SheetID, cfg.SheetGID)
	log.Printf("  poll interval: %s", cfg.SheetPollInterval)
	log.Printf("  hydration interval: %s", cfg.HydrationInterval)
	log.Printf("  workers: %d, batch: %d", cfg.Workers, cfg.HydrationBatch)

	// Resume from existing data file if present
	var state *prdata.DataFile
	if existing, err := prdata.LoadDataFile(cfg.OutputPath); err == nil {
		log.Printf("resumed from existing data file: %d PRs", len(existing.PRs))
		// Backfill PR numbers from URLs
		for i := range existing.PRs {
			if existing.PRs[i].Number == 0 {
				if _, _, n, err := ghpkg.ParsePRURL(existing.PRs[i].URL); err == nil {
					existing.PRs[i].Number = n
				}
			}
		}
		state = existing
	} else {
		state = &prdata.DataFile{Version: 1}
	}

	blob := storage.NewAzureBlobClient(cfg.AzureStorageAccount, cfg.AzureStorageKey, cfg.AzureContainer)
	if blob.Enabled() {
		log.Printf("  azure storage: %s/%s", cfg.AzureStorageAccount, cfg.AzureContainer)
	}

	svc := &service{
		cfg:   cfg,
		state: state,
		blob:  blob,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})

	go func() {
		svc.runSheetPoller(ctx)
		done <- struct{}{}
	}()

	go func() {
		svc.runHydrator(ctx)
		done <- struct{}{}
	}()

	<-sigCh
	log.Printf("shutting down...")
	cancel()
	<-done
	<-done
	log.Printf("shutdown complete")
}

func runSetup() {
	// Interactive Google OAuth setup
	cfg := loadEnvConfig()
	ctx := context.Background()

	log.Printf("Running Google OAuth setup...")
	log.Printf("Credentials: %s", cfg.GoogleSecret)
	log.Printf("Token will be saved to: %s", cfg.GoogleToken)

	// The SheetsClient will trigger the interactive OAuth flow if no token exists
	_, err := sheetsClientForSetup(ctx, cfg.GoogleSecret, cfg.GoogleToken)
	if err != nil {
		log.Fatalf("Setup failed: %v", err)
	}
	log.Printf("Google OAuth setup complete! Token saved to %s", cfg.GoogleToken)
}

func loadEnvConfig() serviceConfig {
	cfg := serviceConfig{
		OutputPath:          envOrDefault("OUTPUT_PATH", "data/prs.json"),
		SheetID:             os.Getenv("SHEET_ID"),
		SheetGID:            envInt64("SHEET_GID", 0),
		GoogleSecret:        envOrDefault("GOOGLE_SECRET", "config/client_secret.json"),
		GoogleToken:         envOrDefault("GOOGLE_TOKEN", "config/google-token.json"),
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
		SheetPollInterval:   envDuration("SHEET_POLL_INTERVAL", 2*time.Minute),
		HydrationInterval:   envDuration("HYDRATION_INTERVAL", 5*time.Second),
		Workers:             envInt("WORKERS", 5),
		HydrationBatch:      envInt("HYDRATION_BATCH_SIZE", 10),
		AzureStorageAccount: os.Getenv("AZURE_STORAGE_ACCOUNT"),
		AzureStorageKey:     os.Getenv("AZURE_STORAGE_KEY"),
		AzureContainer:      envOrDefault("AZURE_CONTAINER", "prdata"),
	}

	if cfg.SheetID == "" {
		log.Fatal("SHEET_ID is required")
	}
	if cfg.GoogleSecret == "" {
		log.Fatal("GOOGLE_SECRET is required")
	}
	if cfg.GitHubToken == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}

	return cfg
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envInt(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultValue
	}
	return n
}

func envInt64(key string, defaultValue int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return defaultValue
	}
	return n
}

func envDuration(key string, defaultValue time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultValue
	}
	return d
}

func sheetsClientForSetup(ctx context.Context, credsPath, tokenPath string) (interface{}, error) {
	// Import sheets package to trigger OAuth flow
	_, err := setupSheetsClient(ctx, credsPath, tokenPath)
	if err != nil {
		return nil, fmt.Errorf("sheets client setup: %w", err)
	}
	return nil, nil
}
