package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	gh "github.com/google/go-github/v58/github"
	"github.com/joho/godotenv"
	"github.com/rdalbuquerque/pr-filter/internal/ai"
	ghpkg "github.com/rdalbuquerque/pr-filter/internal/github"
	"github.com/rdalbuquerque/pr-filter/internal/prdata"
	"github.com/rdalbuquerque/pr-filter/internal/storage"
)

type evalConfig struct {
	AnthropicKey string
	GitHubToken  string
	Model        string
	CostLimit    float64
	Interval     time.Duration
	BatchSize    int
	DataPath     string
	EvalPath     string
	// Azure Blob Storage
	AzureStorageAccount string
	AzureStorageKey     string
	AzureContainer      string
}

func main() {
	// Load .env file if present (non-fatal if missing)
	_ = godotenv.Load()

	cfg := loadConfig()

	if cfg.AnthropicKey == "" {
		log.Fatal("ANTHROPIC_API_KEY is required")
	}
	if cfg.GitHubToken == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}

	log.Printf("pr-evaluator starting")
	log.Printf("  model:      %s", cfg.Model)
	log.Printf("  cost limit: $%.2f", cfg.CostLimit)
	log.Printf("  interval:   %s", cfg.Interval)
	log.Printf("  batch:      %d", cfg.BatchSize)
	log.Printf("  data path:  %s", cfg.DataPath)
	log.Printf("  eval path:  %s", cfg.EvalPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	evaluator := ai.NewEvaluator(ai.EvaluatorConfig{
		APIKey:   cfg.AnthropicKey,
		Model:    cfg.Model,
		LimitUSD: cfg.CostLimit,
	})

	ghClient, err := ghpkg.NewGitHubClientFromToken(ctx, cfg.GitHubToken)
	if err != nil {
		log.Fatalf("create GitHub client: %v", err)
	}

	blob := storage.NewAzureBlobClient(cfg.AzureStorageAccount, cfg.AzureStorageKey, cfg.AzureContainer)
	if blob.Enabled() {
		log.Printf("  azure storage: %s/%s", cfg.AzureStorageAccount, cfg.AzureContainer)
	}

	// Run immediately, then on interval
	runEvalCycle(ctx, cfg, evaluator, ghClient, blob)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down")
			return
		case <-ticker.C:
			runEvalCycle(ctx, cfg, evaluator, ghClient, blob)
		}
	}
}

func runEvalCycle(ctx context.Context, cfg evalConfig, evaluator *ai.Evaluator, ghClient *gh.Client, blob *storage.AzureBlobClient) {
	// Load current PR data
	df, err := prdata.LoadDataFile(cfg.DataPath)
	if err != nil {
		log.Printf("[eval] cannot read data file: %v", err)
		return
	}

	// Load or create evaluations file
	evFile, err := prdata.LoadAIEvaluationsFile(cfg.EvalPath)
	if err != nil {
		log.Printf("[eval] cannot read evaluations file: %v", err)
		return
	}
	if evFile == nil {
		evFile = prdata.NewAIEvaluationsFile(cfg.CostLimit)
	}
	// Always update the limit from config
	evFile.CostTracking.LimitUSD = cfg.CostLimit

	// Check if budget is already exceeded
	if evFile.CostTracking.TotalCostUSD >= evFile.CostTracking.LimitUSD {
		log.Printf("[eval] budget exhausted: $%.4f / $%.2f — skipping",
			evFile.CostTracking.TotalCostUSD, evFile.CostTracking.LimitUSD)
		return
	}

	// Find candidates: hydrated, passes filter, not taken, not already evaluated
	candidates := findCandidates(df.PRs, evFile.Evaluations)
	if len(candidates) == 0 {
		log.Printf("[eval] no new candidates to evaluate")
		return
	}

	totalCandidates := len(candidates)

	// Limit to batch size
	if len(candidates) > cfg.BatchSize {
		candidates = candidates[:cfg.BatchSize]
	}

	log.Printf("[eval] evaluating %d/%d candidates (budget: $%.4f / $%.2f)",
		len(candidates), totalCandidates, evFile.CostTracking.TotalCostUSD, evFile.CostTracking.LimitUSD)

	evaluated, recommended, skippedHeuristic, skippedBudget := 0, 0, 0, 0

	for _, pr := range candidates {
		if ctx.Err() != nil {
			break
		}

		// Skip if file breakdown not yet available (fetcher hasn't backfilled)
		if len(pr.FileBreakdown) == 0 {
			continue
		}

		// Run heuristic pre-filter
		hResult := ai.RunHeuristics(pr.FileBreakdown)
		if !hResult.Pass {
			evFile.Evaluations[pr.URL] = prdata.AIEvaluation{
				Recommended:   false,
				Score:         0,
				Reasoning:     hResult.Reason,
				EvaluatedAt:   time.Now(),
				HeuristicOnly: true,
			}
			skippedHeuristic++
			log.Printf("[eval] %s — heuristic skip: %s", pr.URL, hResult.Reason)
			continue
		}

		// Check cost limit before API call
		estInput := 1200 // conservative estimate
		estOutput := 150
		estCost := ai.EstimateCost(estInput, estOutput)
		if evFile.CostTracking.TotalCostUSD+estCost > evFile.CostTracking.LimitUSD {
			log.Printf("[eval] budget would be exceeded ($%.4f + ~$%.4f > $%.2f) — stopping",
				evFile.CostTracking.TotalCostUSD, estCost, evFile.CostTracking.LimitUSD)
			skippedBudget++
			break
		}

		// Fetch issue body from GitHub
		issueBody := fetchIssueBody(ctx, ghClient, pr.ResolvedIssue)

		// Call AI evaluator
		eval, inputToks, outputToks, err := evaluator.EvaluatePR(ctx, pr, issueBody)
		if err != nil {
			log.Printf("[eval] AI error for %s: %v", pr.URL, err)
			// Still count tokens if we got them (partial response)
			if inputToks > 0 || outputToks > 0 {
				cost := ai.CostFromTokens(inputToks, outputToks)
				evFile.CostTracking.TotalInputTokens += inputToks
				evFile.CostTracking.TotalOutputTokens += outputToks
				evFile.CostTracking.TotalCostUSD += cost
			}
			continue
		}

		// Update cost tracking with actual usage
		cost := ai.CostFromTokens(inputToks, outputToks)
		evFile.CostTracking.TotalInputTokens += inputToks
		evFile.CostTracking.TotalOutputTokens += outputToks
		evFile.CostTracking.TotalCostUSD += cost

		evFile.Evaluations[pr.URL] = eval
		evaluated++
		if eval.Recommended {
			recommended++
		}

		rec := "skip"
		if eval.Recommended {
			rec = "RECOMMEND"
		}
		log.Printf("[eval] %s — %s score=%d (%d+%d tokens, $%.4f) %s",
			pr.URL, rec, eval.Score, inputToks, outputToks, cost, eval.Reasoning)
	}

	// Save evaluations
	if err := prdata.SaveAIEvaluationsFile(cfg.EvalPath, evFile); err != nil {
		log.Printf("[eval] save error: %v", err)
	} else if blob.Enabled() {
		data, err := prdata.MarshalAIEvaluationsFile(evFile)
		if err != nil {
			log.Printf("[azure] marshal error: %v", err)
		} else if err := blob.Upload(ctx, "ai-evaluations.json", data); err != nil {
			log.Printf("[azure] upload error: %v", err)
		} else {
			log.Printf("[azure] uploaded ai-evaluations.json (%d bytes)", len(data))
		}
	}

	log.Printf("[eval] cycle done: %d AI-evaluated (%d recommended), %d heuristic-skipped, %d budget-skipped | budget: $%.4f / $%.2f",
		evaluated, recommended, skippedHeuristic, skippedBudget,
		evFile.CostTracking.TotalCostUSD, evFile.CostTracking.LimitUSD)
}

const noBreakdownReason = "no file breakdown available"

func findCandidates(prs []prdata.PRInfo, evaluated map[string]prdata.AIEvaluation) []prdata.PRInfo {
	var candidates []prdata.PRInfo
	for _, pr := range prs {
		if pr.Hydration < 2 || !pr.PassesFilter || pr.Taken {
			continue
		}
		if prev, exists := evaluated[pr.URL]; exists {
			// Re-evaluate if previously skipped due to missing file breakdown
			// and we now have the data
			if prev.HeuristicOnly && prev.Reasoning == noBreakdownReason && len(pr.FileBreakdown) > 0 {
				candidates = append(candidates, pr)
			}
			continue
		}
		candidates = append(candidates, pr)
	}
	return candidates
}

func fetchIssueBody(ctx context.Context, client *gh.Client, issueURL string) string {
	if issueURL == "" {
		return ""
	}

	owner, repo, number, err := ghpkg.ParseIssueURL(issueURL)
	if err != nil {
		return ""
	}

	issue, _, err := client.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		log.Printf("[eval] fetch issue %s: %v", issueURL, err)
		return ""
	}

	return issue.GetBody()
}

func loadConfig() evalConfig {
	cfg := evalConfig{
		AnthropicKey:        os.Getenv("ANTHROPIC_API_KEY"),
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
		Model:               envOrDefault("AI_MODEL", "claude-haiku-4-5-20251001"),
		CostLimit:           envFloat("AI_COST_LIMIT", 5.00),
		Interval:            envDuration("AI_EVAL_INTERVAL", 30*time.Second),
		BatchSize:            envInt("AI_EVAL_BATCH", 5),
		DataPath:            envOrDefault("DATA_PATH", "data/prs.json"),
		EvalPath:            envOrDefault("EVAL_PATH", "data/ai-evaluations.json"),
		AzureStorageAccount: os.Getenv("AZURE_STORAGE_ACCOUNT"),
		AzureStorageKey:     os.Getenv("AZURE_STORAGE_KEY"),
		AzureContainer:      envOrDefault("AZURE_CONTAINER", "prdata"),
	}
	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Printf("warning: invalid %s=%q, using default %.2f", key, v, def)
		return def
	}
	return f
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("warning: invalid %s=%q, using default %d", key, v, def)
		return def
	}
	return i
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("warning: invalid %s=%q, using default %s", key, v, def)
		return def
	}
	return d
}

func init() {
	log.SetFlags(log.Ltime)
}
