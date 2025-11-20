package cmd

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lehigh-university-libraries/htr/internal/utils"
	"github.com/lehigh-university-libraries/htr/pkg/azure"
	"github.com/lehigh-university-libraries/htr/pkg/claude"
	"github.com/lehigh-university-libraries/htr/pkg/gemini"
	"github.com/lehigh-university-libraries/htr/pkg/ollama"
	"github.com/lehigh-university-libraries/htr/pkg/openai"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"
)

type EvalConfig struct {
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	Prompt         string        `json:"prompt"`
	Temperature    float64       `json:"temperature"`
	Timeout        time.Duration `json:"timeout"`
	CSVPath        string        `json:"csv_path"`
	TestRows       []int         `json:"rows"`
	Timestamp      string        `json:"timestamp"`
	IgnorePatterns []string      `json:"ignore_patterns,omitempty"`

	SingleLine    bool   `json:"single_line,omitempty"`
	MaxResolution string `json:"max_resolution,omitempty"`
}

type EvalResult struct {
	Identifier            string  `json:"identifier"`
	ImagePath             string  `json:"image_path"`
	TranscriptPath        string  `json:"transcript_path"`
	Public                bool    `json:"public"`
	ProviderResponse      string  `json:"provider_response"`
	CharacterSimilarity   float64 `json:"character_similarity"`
	CharacterAccuracy     float64 `json:"character_accuracy"`
	WordSimilarity        float64 `json:"word_similarity"`
	WordAccuracy          float64 `json:"word_accuracy"`
	WordErrorRate         float64 `json:"word_error_rate"`
	TotalWordsOriginal    int     `json:"total_words_original"`
	TotalWordsTranscribed int     `json:"total_words_transcribed"`
	CorrectWords          int     `json:"correct_words"`
	Substitutions         int     `json:"substitutions"`
	Deletions             int     `json:"deletions"`
	Insertions            int     `json:"insertions"`
	IgnoredCharsCount     int     `json:"ignored_chars_count"`
	InputTokens           int     `json:"input_tokens,omitempty"`
	OutputTokens          int     `json:"output_tokens,omitempty"`
}

type EvalSummary struct {
	Config  EvalConfig   `json:"config"`
	Results []EvalResult `json:"results"`
}

type ModelSummary struct {
	Model             string
	Provider          string
	Timestamp         string
	TotalEvaluations  int
	AvgCharSimilarity float64
	AvgCharAccuracy   float64
	AvgWordSimilarity float64
	AvgWordAccuracy   float64
	AvgWordErrorRate  float64
	AvgInputTokens    float64
	AvgOutputTokens   float64
	PageCost          float64
}

// Provider registry for managing all providers
var providerRegistry *providers.Registry

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Evaluate OCR performance using vision models",
	Long: `Evaluate OCR performance by comparing vision model outputs with ground truth transcripts.

You can either provide individual flags or use a previous evaluation config file.

HANDLING UNKNOWN CHARACTERS:

When ground truth contains characters that cannot be deciphered, use the --ignore flag to mark them.
The image still contains these characters, so the LLM will transcribe them as something.
HTR automatically skips the corresponding LLM output when calculating accuracy metrics.

- If ignore pattern is a standalone word (surrounded by spaces): skip next word in transcription
- If ignore pattern is within a word: skip next character in transcription

Examples:
  # Mark unknown characters with pipe
  htr eval --provider openai --model gpt-4o --prompt "Extract text" --csv data.csv --ignore '|'

  # Use multiple ignore patterns
  htr eval --provider gemini --model gemini-1.5-flash --prompt "Extract text" --csv data.csv --ignore '|' --ignore ','

Ground truth examples:
  GT: "The quick | fox"     Trans: "The quick brown fox"     -> Compares "The quick fox" (skips "brown")
  GT: "d|te"                Trans: "date"                    -> Compares "dte" (skips "a")
  GT: "The | cat , jumped"  Trans: "The quick cat suddenly jumped"  -> Compares "The cat jumped"`,
	RunE: runEval,
}

var summaryCmd = &cobra.Command{
	Use:   "summary [eval-file]",
	Short: "Print summary statistics from an existing evaluation file",
	Long: `Print summary statistics from an existing evaluation file in the evals/ directory.

If no file is specified, lists available evaluation files.`,
	RunE: runSummary,
	Args: cobra.MaximumNArgs(1),
}

var csvCmd = &cobra.Command{
	Use:   "csv",
	Short: "Export evaluation results as CSV sorted by model performance",
	Long: `Scan all YAML files in the evals directory and export summary statistics as CSV.

Results are sorted by word accuracy (best to worst) and printed to terminal.

If --input-price and --output-price are provided, a PageCost column will be included.`,
	RunE: runCSV,
	Args: cobra.NoArgs,
}

var backfillCmd = &cobra.Command{
	Use:   "backfill",
	Short: "Backfill metrics for existing evaluation files",
	Long: `Scan all YAML files in the evals directory and recalculate metrics for existing evaluations.

This command reads the provider response and ground truth from existing evaluation files,
recalculates metrics (character accuracy, word similarity), and updates the YAML files in place.

By default, uses the --single-line and --ignore flags saved in each evaluation's config.
You can override these by passing --single-line or --ignore flags to this command.

This is useful after upgrading to a version with improved metric calculations.`,
	RunE: runBackfill,
	Args: cobra.NoArgs,
}

var costCmd = &cobra.Command{
	Use:   "cost [eval-file]",
	Short: "Calculate cost estimates based on token usage from an evaluation file",
	Long: `Calculate cost estimates based on token usage from an evaluation file.

This command reads an evaluation file containing token usage data and calculates:
- Average input and output tokens per document
- Estimated cost for a given number of documents

Requires --input-price and --output-price flags (cost per million tokens).
Optionally specify --doc-count to estimate cost for a specific number of documents.`,
	RunE: runCost,
	Args: cobra.ExactArgs(1),
}

var (
	evalProvider    string
	evalModel       string
	evalPrompt      string
	evalTemperature float64
	evalTimeout     time.Duration
	evalCSVPath     string
	evalConfigPath  string
	evalTemplate    string
	dir             string
	rows            []int
	ignorePatterns  []string
	singleLine      bool
	maxResolution   string

	// from https://ai.google.dev/gemini-api/docs/media-resolution#available_resolution_values
	allowedMediaResolutions = []string{
		"MEDIA_RESOLUTION_UNSPECIFIED",
		"MEDIA_RESOLUTION_LOW",
		"MEDIA_RESOLUTION_MEDIUM",
		"MEDIA_RESOLUTION_HIGH",
		"MEDIA_RESOLUTION_ULTRA_HIGH",
	}

	// Backfill command flags
	backfillIgnorePatterns []string
	backfillSingleLine     bool
	backfillOverride       bool

	// Cost command flags
	costInputPrice  float64
	costOutputPrice float64
	costDocCount    int

	// CSV command flags
	csvInputPrice  float64
	csvOutputPrice float64
)

func init() {
	// Initialize provider registry
	providerRegistry = providers.NewRegistry()
	providerRegistry.Register(openai.New())
	providerRegistry.Register(azure.New())
	providerRegistry.Register(claude.New())
	providerRegistry.Register(gemini.New())
	providerRegistry.Register(ollama.New())

	RootCmd.AddCommand(evalCmd)
	RootCmd.AddCommand(summaryCmd)
	RootCmd.AddCommand(csvCmd)
	RootCmd.AddCommand(backfillCmd)
	RootCmd.AddCommand(costCmd)

	// Eval command flags
	evalCmd.Flags().StringVar(&evalProvider, "provider", "openai", "Provider to use: openai, azure, claude, gemini, ollama")
	evalCmd.Flags().StringVarP(&evalModel, "model", "m", "gpt-4o", "Model to use")
	evalCmd.Flags().StringVarP(&evalPrompt, "prompt", "p", "", "Prompt to send to the provider")
	evalCmd.Flags().Float64VarP(&evalTemperature, "temperature", "t", 0.0, "Temperature for API")
	evalCmd.Flags().DurationVar(&evalTimeout, "timeout", 5*time.Minute, "Timeout for API requests (e.g., 5m, 30s, 1h)")
	evalCmd.Flags().StringVarP(&evalCSVPath, "csv", "c", "", "Path to CSV file with evaluation data")
	evalCmd.Flags().StringVar(&evalConfigPath, "config", "", "Path to previous evaluation config file to rerun")
	evalCmd.Flags().StringVar(&evalTemplate, "template", "", "Custom JSON template file for API (optional)")
	evalCmd.Flags().StringVar(&dir, "dir", "./", "Prepend your CSV file paths with a directory")
	evalCmd.Flags().IntSliceVar(&rows, "rows", []int{}, "A list of row numbers to run the test on")
	evalCmd.Flags().StringSliceVar(&ignorePatterns, "ignore", []string{}, "Characters or strings to ignore in ground truth (e.g., --ignore '|' --ignore ',')")

	evalCmd.Flags().BoolVar(&singleLine, "single-line", false, "Convert ground truth and transcripts to single line (remove newlines, carriage returns, tabs, and normalize spaces)")
	evalCmd.Flags().StringVar(&maxResolution, "gemini-max-resolution", "MEDIA_RESOLUTION_UNSPECIFIED", "Max resolution for Gemini models (e.g., MEDIA_RESOLUTION_HIGH)")

	evalCmd.MarkFlagsRequiredTogether("csv", "prompt")
	evalCmd.MarkFlagsMutuallyExclusive("csv", "config")

	// Backfill command flags
	backfillCmd.Flags().StringSliceVar(&backfillIgnorePatterns, "ignore", []string{}, "Override ignore patterns for all evaluations (e.g., --ignore '|' --ignore ',')")
	backfillCmd.Flags().BoolVar(&backfillSingleLine, "single-line", false, "Override single-line flag for all evaluations")
	backfillCmd.Flags().BoolVar(&backfillOverride, "override", false, "Force override of saved flags (use CLI flags for all evaluations)")

	// Cost command flags
	costCmd.Flags().Float64Var(&costInputPrice, "input-price", 0.0, "Cost per million input tokens (e.g., 1.25 for $1.25/1M)")
	costCmd.Flags().Float64Var(&costOutputPrice, "output-price", 0.0, "Cost per million output tokens (e.g., 10.0 for $10.00/1M)")
	costCmd.Flags().IntVar(&costDocCount, "doc-count", 1000, "Number of documents to estimate cost for")
	_ = costCmd.MarkFlagRequired("input-price")
	_ = costCmd.MarkFlagRequired("output-price")

	// CSV command flags
	csvCmd.Flags().Float64Var(&csvInputPrice, "input-price", 0.0, "Cost per million input tokens (optional)")
	csvCmd.Flags().Float64Var(&csvOutputPrice, "output-price", 0.0, "Cost per million output tokens (optional)")
}

func runEval(cmd *cobra.Command, args []string) error {
	var config EvalConfig
	var err error

	// Determine if we're using a config file or individual flags
	if evalConfigPath != "" {
		config, err = loadEvalConfig(evalConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		fmt.Printf("Loaded configuration from %s\n", evalConfigPath)
	} else {
		config = EvalConfig{
			Provider:       evalProvider,
			Model:          evalModel,
			Prompt:         evalPrompt,
			Temperature:    evalTemperature,
			Timeout:        evalTimeout,
			CSVPath:        evalCSVPath,
			Timestamp:      time.Now().Format("2006-01-02_15-04-05"),
			IgnorePatterns: ignorePatterns,

			SingleLine:    singleLine,
			MaxResolution: maxResolution,
		}
	}

	if !slices.Contains(allowedMediaResolutions, config.MaxResolution) {
		return fmt.Errorf("invalid --gemini-max-resolution value '%s'. Allowed values are: %s", config.MaxResolution, strings.Join(allowedMediaResolutions, ", "))
	}

	testRows, err := cmd.Flags().GetIntSlice("rows")
	if err != nil {
		return fmt.Errorf("failed to fetch rows flag: %w", err)
	}
	config.TestRows = testRows
	evalsDir := "evals"
	if err := os.MkdirAll(evalsDir, 0755); err != nil {
		return fmt.Errorf("failed to create evals directory: %w", err)
	}

	results, err := processEvaluation(config)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}

	summary := EvalSummary{
		Config:  config,
		Results: results,
	}

	m := strings.ReplaceAll(config.Model, ":", "_")
	outputPath := filepath.Join(evalsDir, fmt.Sprintf("%s.yaml", m))
	if err := saveEvalResults(summary, outputPath); err != nil {
		return fmt.Errorf("failed to save results: %w", err)
	}

	fmt.Printf("\nEvaluation completed. Results saved to: %s\n", outputPath)
	printSummaryStats(results)

	return nil
}

func runSummary(cmd *cobra.Command, args []string) error {
	evalsDir := "evals"

	// If no argument provided, list available eval files
	if len(args) == 0 {
		files, err := filepath.Glob(filepath.Join(evalsDir, "*.yaml"))
		if err != nil {
			return fmt.Errorf("failed to list eval files: %w", err)
		}

		if len(files) == 0 {
			fmt.Println("No evaluation files found in evals/ directory.")
			return nil
		}

		fmt.Println("Available evaluation files:")
		for _, file := range files {
			fmt.Printf("  %s\n", filepath.Base(file))
		}
		fmt.Println("\nUse: htr summary <filename>")
		return nil
	}

	// Load and display summary for specified file
	evalFile := args[0]
	if !strings.HasSuffix(evalFile, ".yaml") {
		evalFile += ".yaml"
	}

	// If no path separator, assume it's in evals directory
	if !strings.Contains(evalFile, string(filepath.Separator)) {
		evalFile = filepath.Join(evalsDir, evalFile)
	}

	data, err := os.ReadFile(evalFile)
	if err != nil {
		return fmt.Errorf("failed to read eval file %s: %w", evalFile, err)
	}

	var summary EvalSummary
	if err := yaml.Unmarshal(data, &summary); err != nil {
		return fmt.Errorf("failed to parse eval file: %w", err)
	}

	// Display configuration
	fmt.Printf("=== EVALUATION SUMMARY ===\n")
	fmt.Printf("File: %s\n", filepath.Base(evalFile))
	fmt.Printf("Provider: %s\n", summary.Config.Provider)
	fmt.Printf("Model: %s\n", summary.Config.Model)
	fmt.Printf("Temperature: %.1f\n", summary.Config.Temperature)
	fmt.Printf("CSV Path: %s\n", summary.Config.CSVPath)
	fmt.Printf("Timestamp: %s\n", summary.Config.Timestamp)
	fmt.Printf("Total Images Evaluated: %d\n", len(summary.Results))

	// Display summary statistics
	printSummaryStats(summary.Results)

	return nil
}

func runCSV(cmd *cobra.Command, args []string) error {
	evalsDir := "evals"

	// Find all YAML files
	files, err := filepath.Glob(filepath.Join(evalsDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list eval files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No evaluation files found in evals/ directory.")
		return nil
	}

	var modelSummaries []ModelSummary

	// Process each file
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Printf("Warning: failed to read %s: %v\n", file, err)
			continue
		}

		var summary EvalSummary
		if err := yaml.Unmarshal(data, &summary); err != nil {
			fmt.Printf("Warning: failed to parse %s: %v\n", file, err)
			continue
		}

		if len(summary.Results) == 0 {
			continue
		}

		// Get the original flags from config for backward compatibility
		ignorePatterns := summary.Config.IgnorePatterns
		if ignorePatterns == nil {
			ignorePatterns = []string{}
		}
		singleLine := summary.Config.SingleLine

		// Calculate aggregated metrics
		var totalCharSim, totalCharAcc, totalWordSim, totalWordAcc, totalWER float64
		var totalInputTokens, totalOutputTokens int
		for _, result := range summary.Results {
			totalCharSim += result.CharacterSimilarity

			// Backward compatibility: calculate CharacterAccuracy if not present
			charAcc := result.CharacterAccuracy
			if charAcc == 0.0 && result.ProviderResponse != "" {
				// Calculate on the fly using ground truth from TranscriptPath
				// Use the original flags from the evaluation config
				if groundTruth, err := readTextFile(result.TranscriptPath); err == nil {
					metrics := CalculateAccuracyMetrics(groundTruth, result.ProviderResponse, ignorePatterns, singleLine)
					charAcc = metrics.CharacterAccuracy
				}
			}
			totalCharAcc += charAcc

			totalWordSim += result.WordSimilarity
			totalWordAcc += result.WordAccuracy
			totalWER += result.WordErrorRate
			totalInputTokens += result.InputTokens
			totalOutputTokens += result.OutputTokens
		}

		count := float64(len(summary.Results))
		avgInputTokens := float64(totalInputTokens) / count
		avgOutputTokens := float64(totalOutputTokens) / count

		// Calculate page cost if prices are provided
		pageCost := 0.0
		if csvInputPrice > 0 || csvOutputPrice > 0 {
			inputCost := (avgInputTokens / 1_000_000) * csvInputPrice
			outputCost := (avgOutputTokens / 1_000_000) * csvOutputPrice
			pageCost = inputCost + outputCost
		}

		modelSummary := ModelSummary{
			Model:             summary.Config.Model,
			Provider:          summary.Config.Provider,
			Timestamp:         summary.Config.Timestamp,
			TotalEvaluations:  len(summary.Results),
			AvgCharSimilarity: totalCharSim / count,
			AvgCharAccuracy:   totalCharAcc / count,
			AvgWordSimilarity: totalWordSim / count,
			AvgWordAccuracy:   totalWordAcc / count,
			AvgWordErrorRate:  totalWER / count,
			AvgInputTokens:    avgInputTokens,
			AvgOutputTokens:   avgOutputTokens,
			PageCost:          pageCost,
		}

		modelSummaries = append(modelSummaries, modelSummary)
	}

	if len(modelSummaries) == 0 {
		fmt.Println("No valid evaluation data found.")
		return nil
	}

	// Sort by word similarity (best to worst)
	slices.SortFunc(modelSummaries, func(a, b ModelSummary) int {
		if a.AvgWordSimilarity > b.AvgWordSimilarity {
			return -1
		}
		if a.AvgWordSimilarity < b.AvgWordSimilarity {
			return 1
		}
		return 0
	})

	// Determine if we should include PageCost column
	includeCost := csvInputPrice > 0 || csvOutputPrice > 0

	// Print TSV header
	if includeCost {
		fmt.Println("Model\tTotalEvaluations\tAvgCharSimilarity\tAvgCharAccuracy\tAvgWordSimilarity\tAvgWordAccuracy\tAvgWordErrorRate\tAvgInputTokens\tAvgOutputTokens\tPageCost")
	} else {
		fmt.Println("Model\tTotalEvaluations\tAvgCharSimilarity\tAvgCharAccuracy\tAvgWordSimilarity\tAvgWordAccuracy\tAvgWordErrorRate")
	}

	// Print TSV data
	for _, ms := range modelSummaries {
		if includeCost {
			fmt.Printf("%s\t%d\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.2f\t%.2f\t%.6f\n",
				ms.Model,
				ms.TotalEvaluations,
				ms.AvgCharSimilarity,
				ms.AvgCharAccuracy,
				ms.AvgWordSimilarity,
				ms.AvgWordAccuracy,
				ms.AvgWordErrorRate,
				ms.AvgInputTokens,
				ms.AvgOutputTokens,
				ms.PageCost)
		} else {
			fmt.Printf("%s\t%d\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\n",
				ms.Model,
				ms.TotalEvaluations,
				ms.AvgCharSimilarity,
				ms.AvgCharAccuracy,
				ms.AvgWordSimilarity,
				ms.AvgWordAccuracy,
				ms.AvgWordErrorRate)
		}
	}

	return nil
}

func runBackfill(cmd *cobra.Command, args []string) error {
	evalsDir := "evals"

	// Find all YAML files
	files, err := filepath.Glob(filepath.Join(evalsDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list eval files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No evaluation files found in evals/ directory.")
		return nil
	}

	// Check if user wants to override flags
	hasIgnoreFlag := cmd.Flags().Changed("ignore")
	hasSingleLineFlag := cmd.Flags().Changed("single-line")
	useOverride := backfillOverride || hasIgnoreFlag || hasSingleLineFlag

	if useOverride {
		fmt.Printf("Using CLI flags for recalculation:\n")
		if hasIgnoreFlag {
			fmt.Printf("  --ignore: %v\n", backfillIgnorePatterns)
		}
		if hasSingleLineFlag {
			fmt.Printf("  --single-line: %v\n", backfillSingleLine)
		}
		fmt.Println()
	}

	updatedCount := 0
	skippedCount := 0

	// Process each file
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Printf("Warning: failed to read %s: %v\n", file, err)
			continue
		}

		var summary EvalSummary
		if err := yaml.Unmarshal(data, &summary); err != nil {
			fmt.Printf("Warning: failed to parse %s: %v\n", file, err)
			continue
		}

		if len(summary.Results) == 0 {
			continue
		}

		// Determine which flags to use
		var ignorePatterns []string
		var singleLine bool

		if useOverride {
			// Use CLI flags (override)
			if hasIgnoreFlag {
				ignorePatterns = backfillIgnorePatterns
			} else {
				ignorePatterns = []string{}
			}
			if hasSingleLineFlag {
				singleLine = backfillSingleLine
			} else {
				singleLine = false
			}
		} else {
			// Use saved flags from config (default behavior)
			ignorePatterns = summary.Config.IgnorePatterns
			if ignorePatterns == nil {
				ignorePatterns = []string{}
			}
			singleLine = summary.Config.SingleLine
		}

		// Recalculate metrics for all results
		needsUpdate := false
		for i := range summary.Results {
			if summary.Results[i].ProviderResponse == "" {
				continue
			}

			// Read ground truth from stored path
			groundTruth, err := readTextFile(summary.Results[i].TranscriptPath)
			if err != nil {
				fmt.Printf("Warning: failed to read transcript %s: %v\n", summary.Results[i].TranscriptPath, err)
				continue
			}

			// Recalculate all metrics
			metrics := CalculateAccuracyMetrics(groundTruth, summary.Results[i].ProviderResponse, ignorePatterns, singleLine)

			// Check if we need to update any metrics
			if summary.Results[i].CharacterAccuracy != metrics.CharacterAccuracy ||
				summary.Results[i].WordSimilarity != metrics.WordSimilarity {
				summary.Results[i].CharacterAccuracy = metrics.CharacterAccuracy
				summary.Results[i].WordSimilarity = metrics.WordSimilarity
				needsUpdate = true
			}
		}

		if needsUpdate {
			// Save updated YAML
			if err := saveEvalResults(summary, file); err != nil {
				fmt.Printf("Warning: failed to save %s: %v\n", file, err)
				continue
			}
			fmt.Printf("✓ Updated %s\n", filepath.Base(file))
			updatedCount++
		} else {
			skippedCount++
		}
	}

	fmt.Printf("\nBackfill complete:\n")
	fmt.Printf("  Updated: %d files\n", updatedCount)
	fmt.Printf("  Skipped: %d files (metrics already up to date)\n", skippedCount)

	return nil
}

func loadEvalConfig(configPath string) (EvalConfig, error) {
	var summary EvalSummary

	data, err := os.ReadFile(configPath)
	if err != nil {
		return EvalConfig{}, err
	}

	if err := yaml.Unmarshal(data, &summary); err != nil {
		return EvalConfig{}, err
	}

	// Update timestamp for rerun
	summary.Config.Timestamp = time.Now().Format("2006-01-02_15-04-05")

	// Set default timeout if not present (for backward compatibility)
	if summary.Config.Timeout == 0 {
		summary.Config.Timeout = 5 * time.Minute
	}

	return summary.Config, nil
}

func processEvaluation(config EvalConfig) ([]EvalResult, error) {
	// Read CSV file
	file, err := os.Open(config.CSVPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("CSV file is empty")
	}

	// Skip header row if present
	dataRows := records
	if len(records) > 0 && strings.EqualFold(records[0][0], "image") {
		dataRows = records[1:]
	}

	if len(config.TestRows) == 0 {
		config.TestRows = []int{}
		for i := 0; i < len(dataRows); i++ {
			config.TestRows = append(config.TestRows, i)
		}
	}

	var results []EvalResult
	for i, row := range dataRows {
		if !slices.Contains(config.TestRows, i) {
			slog.Warn("Skipping row", "row", i+1)
			continue
		}
		if len(row) < 3 {
			slog.Warn("Insufficient columns", "row", i+1)
			continue
		}

		result, err := processRow(row, config)
		if err != nil {
			slog.Error("Error processing row", "row", i+1, "err", utils.MaskSensitiveError(err))
			continue
		}

		results = append(results, result)

		printRowResult(result)
	}

	return results, nil
}

func processRow(row []string, config EvalConfig) (EvalResult, error) {
	imagePath := filepath.Join(dir, strings.TrimSpace(row[0]))
	transcriptPath := filepath.Join(dir, strings.TrimSpace(row[1]))
	publicStr := strings.TrimSpace(row[2])

	public, err := strconv.ParseBool(publicStr)
	if err != nil && publicStr != "0" && publicStr != "1" {
		return EvalResult{}, fmt.Errorf("invalid public value: %s", publicStr)
	}
	if publicStr == "1" {
		public = true
	}

	groundTruth, err := readTextFile(transcriptPath)
	if err != nil {
		return EvalResult{}, fmt.Errorf("failed to read transcript: %w", err)
	}

	imageBase64, err := getImageAsBase64(imagePath)
	if err != nil {
		return EvalResult{}, fmt.Errorf("failed to process image: %w", err)
	}

	providerResponse, usage, err := extractTextWithProvider(config, imagePath, imageBase64)
	if err != nil {
		return EvalResult{}, fmt.Errorf("provider API call failed: %w", err)
	}

	metrics := CalculateAccuracyMetrics(groundTruth, providerResponse, ignorePatterns, singleLine)

	result := EvalResult{
		Identifier:            filepath.Base(imagePath),
		ImagePath:             imagePath,
		TranscriptPath:        transcriptPath,
		Public:                public,
		ProviderResponse:      providerResponse,
		CharacterSimilarity:   metrics.CharacterSimilarity,
		CharacterAccuracy:     metrics.CharacterAccuracy,
		WordSimilarity:        metrics.WordSimilarity,
		WordAccuracy:          metrics.WordAccuracy,
		WordErrorRate:         metrics.WordErrorRate,
		TotalWordsOriginal:    metrics.TotalWordsOriginal,
		TotalWordsTranscribed: metrics.TotalWordsTranscribed,
		CorrectWords:          metrics.CorrectWords,
		Substitutions:         metrics.Substitutions,
		Deletions:             metrics.Deletions,
		Insertions:            metrics.Insertions,
		IgnoredCharsCount:     metrics.IgnoredCharsCount,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
	}

	return result, nil
}

func readTextFile(path string) (string, error) {
	// Check if it's a URL
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		resp, err := http.Get(path)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Local file
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func getImageAsBase64(imagePath string) (string, error) {
	var imageData []byte
	var err error

	// Check if it's a URL
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		resp, err := http.Get(imagePath)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		imageData, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
	} else {
		// Local file
		imageData, err = os.ReadFile(imagePath)
		if err != nil {
			return "", err
		}
	}

	return base64.StdEncoding.EncodeToString(imageData), nil
}

// extractTextWithProvider extracts text using the appropriate provider
func extractTextWithProvider(config EvalConfig, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	// Get provider from registry
	provider, err := providerRegistry.Get(config.Provider)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("unsupported provider: %s", config.Provider)
	}

	// Convert EvalConfig to providers.Config
	providerConfig := providers.Config{
		Provider:    config.Provider,
		Model:       config.Model,
		Prompt:      config.Prompt,
		Temperature: config.Temperature,
		Timeout:     config.Timeout,
	}

	// Validate configuration
	if err := provider.ValidateConfig(providerConfig); err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("invalid configuration for provider %s: %w", config.Provider, err)
	}

	// Extract text using the provider
	return provider.ExtractText(context.Background(), providerConfig, imagePath, imageBase64)
}

func saveEvalResults(summary EvalSummary, outputPath string) error {
	data, err := yaml.Marshal(summary)
	if err != nil {
		return err
	}

	return os.WriteFile(outputPath, data, 0644)
}

func printRowResult(result EvalResult) {
	fmt.Printf("\n=== Results for %s ===\n", result.Identifier)
	fmt.Printf("Image: %s\n", result.ImagePath)
	fmt.Printf("Transcript: %s\n", result.TranscriptPath)
	fmt.Printf("Character Similarity: %.3f\n", result.CharacterSimilarity)
	fmt.Printf("Character Accuracy: %.3f\n", result.CharacterAccuracy)
	fmt.Printf("Word Similarity: %.3f\n", result.WordSimilarity)
	fmt.Printf("Word Accuracy: %.3f\n", result.WordAccuracy)
	fmt.Printf("Word Error Rate: %.3f\n", result.WordErrorRate)
	fmt.Printf("Total Words (Original): %d\n", result.TotalWordsOriginal)
	fmt.Printf("Total Words (Transcribed): %d\n", result.TotalWordsTranscribed)
	fmt.Printf("Correct Words: %d\n", result.CorrectWords)
	fmt.Printf("Substitutions: %d\n", result.Substitutions)
	fmt.Printf("Deletions: %d\n", result.Deletions)
	fmt.Printf("Insertions: %d\n", result.Insertions)
	if result.IgnoredCharsCount > 0 {
		fmt.Printf("Ignored Characters: %d\n", result.IgnoredCharsCount)
	}
}

func printSummaryStats(results []EvalResult) {
	if len(results) == 0 {
		return
	}

	var totalCharSim, totalCharAcc, totalWordSim, totalWordAcc, totalWER float64

	for _, result := range results {
		totalCharSim += result.CharacterSimilarity
		totalCharAcc += result.CharacterAccuracy
		totalWordSim += result.WordSimilarity
		totalWordAcc += result.WordAccuracy
		totalWER += result.WordErrorRate
	}

	count := float64(len(results))

	fmt.Printf("\n=== SUMMARY STATISTICS ===\n")
	fmt.Printf("Total Evaluations: %d\n", len(results))
	fmt.Printf("Average Character Similarity: %.3f\n", totalCharSim/count)
	fmt.Printf("Average Character Accuracy: %.3f\n", totalCharAcc/count)
	fmt.Printf("Average Word Similarity: %.3f\n", totalWordSim/count)
	fmt.Printf("Average Word Accuracy: %.3f\n", totalWordAcc/count)
	fmt.Printf("Average Word Error Rate: %.3f\n", totalWER/count)
}

// applyIgnorePatterns handles unknown characters in ground truth.
//
// When a character cannot be deciphered in the ground truth, it's marked with an ignore pattern (e.g., "|").
// The image still contains some character, so the LLM will transcribe it as something.
// This function:
// 1. Counts all ignored pattern occurrences in ground truth
// 2. Removes ignored patterns from ground truth
// 3. Skips corresponding characters/words in the LLM transcription:
//   - If ignore pattern is surrounded by spaces (standalone word): skip next word in transcription
//   - If ignore pattern is within a word: skip next character in transcription
//
// Example 1 (standalone): GT="hello | world", Trans="hello foo world"
//
//	-> processedGT="hello world", processedTrans="hello world", ignoredCount=1
//
// Example 2 (within word): GT="hel|o", Trans="hello"
//
//	-> processedGT="helo", processedTrans="helo", ignoredCount=1
//
// Returns: (processedGroundTruth, processedTranscription, ignoredCharCount)
func applyIgnorePatterns(groundTruth, transcription string, ignorePatterns []string) (string, string, int) {
	if len(ignorePatterns) == 0 {
		return groundTruth, transcription, 0
	}

	// Count total ignored characters in ground truth
	ignoredCount := 0
	for _, pattern := range ignorePatterns {
		ignoredCount += strings.Count(groundTruth, pattern) * len(pattern)
	}

	// Process character by character and word by word
	var processedGT strings.Builder
	var processedTrans strings.Builder

	gtRunes := []rune(groundTruth)
	transRunes := []rune(transcription)

	gtIdx := 0
	transIdx := 0

	for gtIdx < len(gtRunes) {
		// Check if current position matches any ignore pattern
		matchedPattern := ""
		for _, pattern := range ignorePatterns {
			patternRunes := []rune(pattern)
			if gtIdx+len(patternRunes) <= len(gtRunes) {
				match := true
				for i, pr := range patternRunes {
					if gtRunes[gtIdx+i] != pr {
						match = false
						break
					}
				}
				if match {
					matchedPattern = pattern
					break
				}
			}
		}

		if matchedPattern != "" {
			// Found an ignore pattern - determine if it's standalone or within a word
			patternLen := len([]rune(matchedPattern))

			// Check if pattern is surrounded by whitespace (or at boundaries)
			beforeIsSpace := gtIdx == 0 || unicode.IsSpace(gtRunes[gtIdx-1])
			afterIsSpace := gtIdx+patternLen >= len(gtRunes) || unicode.IsSpace(gtRunes[gtIdx+patternLen])

			if beforeIsSpace && afterIsSpace {
				// Standalone word - skip the pattern and skip next word in transcription
				gtIdx += patternLen

				// Skip whitespace in transcription
				for transIdx < len(transRunes) && unicode.IsSpace(transRunes[transIdx]) {
					transIdx++
				}

				// Skip the next word in transcription
				for transIdx < len(transRunes) && !unicode.IsSpace(transRunes[transIdx]) {
					transIdx++
				}
			} else {
				// Pattern within a word - skip the pattern and skip next character in transcription
				gtIdx += patternLen

				// Skip one non-whitespace character in transcription
				if transIdx < len(transRunes) {
					transIdx++
				}
			}
		} else {
			// Normal character - copy to both outputs
			currentChar := gtRunes[gtIdx]
			processedGT.WriteRune(currentChar)

			if transIdx < len(transRunes) {
				processedTrans.WriteRune(transRunes[transIdx])
				transIdx++
			}

			gtIdx++
		}
	}

	return processedGT.String(), processedTrans.String(), ignoredCount
}

func normalizeSpaces(text string) string {
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return text
}

func levenshteinDistance(s1, s2 string) int {
	len1, len2 := len(s1), len(s2)
	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}

	matrix := make([][]int, len1+1)
	for i := range matrix {
		matrix[i] = make([]int, len2+1)
	}

	for i := 0; i <= len1; i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			matrix[i][j] = min(
				min(matrix[i-1][j]+1, matrix[i][j-1]+1), // deletion, insertion
				matrix[i-1][j-1]+cost,                   // substitution
			)
		}
	}

	return matrix[len1][len2]
}

func calculateSimilarity(s1, s2 string) float64 {
	maxLen := max(len(s1), len(s2))
	if maxLen == 0 {
		return 1.0
	}
	distance := levenshteinDistance(s1, s2)
	return 1.0 - float64(distance)/float64(maxLen)
}

func calculateWordSimilarity(words1, words2 []string) float64 {
	maxLen := max(len(words1), len(words2))
	if maxLen == 0 {
		return 1.0
	}
	distance := wordLevelLevenshteinDistance(words1, words2)
	return 1.0 - float64(distance)/float64(maxLen)
}

func wordLevelLevenshteinDistance(words1, words2 []string) int {
	len1, len2 := len(words1), len(words2)
	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}

	matrix := make([][]int, len1+1)
	for i := range matrix {
		matrix[i] = make([]int, len2+1)
	}

	for i := 0; i <= len1; i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := 0
			if words1[i-1] != words2[j-1] {
				cost = 1
			}
			matrix[i][j] = min(
				min(matrix[i-1][j]+1, matrix[i][j-1]+1), // deletion, insertion
				matrix[i-1][j-1]+cost,                   // substitution
			)
		}
	}

	return matrix[len1][len2]
}

func calculateWordLevelMetrics(orig, trans []string) (float64, int, int, int, int) {
	m, n := len(orig), len(trans)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 0; i <= m; i++ {
		dp[i][0] = i
	}
	for j := 0; j <= n; j++ {
		dp[0][j] = j
	}

	// Fill DP table
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if orig[i-1] == trans[j-1] {
				dp[i][j] = dp[i-1][j-1] // match
			} else {
				dp[i][j] = 1 + min(
					min(dp[i-1][j], dp[i][j-1]), // deletion, insertion
					dp[i-1][j-1],                // substitution
				)
			}
		}
	}

	// Backtrack to count operations
	i, j := m, n
	substitutions, deletions, insertions, correct := 0, 0, 0, 0

	for i > 0 || j > 0 {
		if i > 0 && j > 0 && orig[i-1] == trans[j-1] {
			correct++
			i--
			j--
		} else if i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+1 {
			substitutions++
			i--
			j--
		} else if i > 0 && dp[i][j] == dp[i-1][j]+1 {
			deletions++
			i--
		} else if j > 0 && dp[i][j] == dp[i][j-1]+1 {
			insertions++
			j--
		}
	}

	totalEdits := substitutions + deletions + insertions
	wer := 0.0
	if m > 0 {
		wer = float64(totalEdits) / float64(m)
	}
	wordAccuracy := 1.0 - wer

	return wordAccuracy, correct, substitutions, deletions, insertions
}

func CalculateAccuracyMetrics(original, transcribed string, ignorePatterns []string, singleLine bool) EvalResult {
	// Apply text transformations first
	origTransformed := original
	transTransformed := transcribed

	// Convert to single line if requested
	if singleLine {
		slog.Debug("Applying --single-line transformation",
			"original_ground_truth", original,
			"original_transcription", transcribed)
		origTransformed = strings.ReplaceAll(origTransformed, "\n", " ")
		origTransformed = strings.ReplaceAll(origTransformed, "\r", " ")
		origTransformed = strings.ReplaceAll(origTransformed, "\t", " ")
		transTransformed = strings.ReplaceAll(transTransformed, "\n", " ")
		transTransformed = strings.ReplaceAll(transTransformed, "\r", " ")
		transTransformed = strings.ReplaceAll(transTransformed, "\t", " ")

		// Normalize multiple spaces to single space
		origTransformed = normalizeSpaces(origTransformed)
		transTransformed = normalizeSpaces(transTransformed)

		slog.Debug("After --single-line transformation",
			"transformed_ground_truth", origTransformed,
			"transformed_transcription", transTransformed)
	}

	// Apply ignore patterns to both texts
	origProcessed, transProcessed, ignoredCount := applyIgnorePatterns(origTransformed, transTransformed, ignorePatterns)

	charSim := calculateSimilarity(origProcessed, transProcessed)

	// Calculate character accuracy (1 - CER)
	origLen := len(origProcessed)
	charDistance := levenshteinDistance(origProcessed, transProcessed)
	charAcc := 1.0
	if origLen > 0 {
		charAcc = 1.0 - float64(charDistance)/float64(origLen)
	}

	origWords := strings.Fields(origProcessed)
	transWords := strings.Fields(transProcessed)
	wordSim := calculateWordSimilarity(origWords, transWords)
	wordAcc, correct, subs, dels, ins := calculateWordLevelMetrics(origWords, transWords)

	wer := 1.0 - wordAcc

	return EvalResult{
		CharacterSimilarity:   charSim,
		CharacterAccuracy:     charAcc,
		WordSimilarity:        wordSim,
		WordAccuracy:          wordAcc,
		WordErrorRate:         wer,
		TotalWordsOriginal:    len(origWords),
		TotalWordsTranscribed: len(transWords),
		CorrectWords:          correct,
		Substitutions:         subs,
		Deletions:             dels,
		Insertions:            ins,
		IgnoredCharsCount:     ignoredCount,
	}
}

func runCost(cmd *cobra.Command, args []string) error {
	evalsDir := "evals"
	evalFile := args[0]

	if !strings.HasSuffix(evalFile, ".yaml") {
		evalFile += ".yaml"
	}

	// If no path separator, assume it's in evals directory
	if !strings.Contains(evalFile, string(filepath.Separator)) {
		evalFile = filepath.Join(evalsDir, evalFile)
	}

	// Read and parse the eval file
	data, err := os.ReadFile(evalFile)
	if err != nil {
		return fmt.Errorf("failed to read eval file %s: %w", evalFile, err)
	}

	var summary EvalSummary
	if err := yaml.Unmarshal(data, &summary); err != nil {
		return fmt.Errorf("failed to parse eval file: %w", err)
	}

	// Calculate average tokens per document
	var totalInputTokens, totalOutputTokens int
	var docsWithTokens int

	for _, result := range summary.Results {
		if result.InputTokens > 0 || result.OutputTokens > 0 {
			totalInputTokens += result.InputTokens
			totalOutputTokens += result.OutputTokens
			docsWithTokens++
		}
	}

	if docsWithTokens == 0 {
		return fmt.Errorf("no token usage data found in evaluation file (run eval with a provider that supports token tracking)")
	}

	avgInputTokens := float64(totalInputTokens) / float64(docsWithTokens)
	avgOutputTokens := float64(totalOutputTokens) / float64(docsWithTokens)

	// Calculate costs
	// Price is per million tokens, so divide by 1,000,000
	inputCostPerDoc := (avgInputTokens / 1_000_000) * costInputPrice
	outputCostPerDoc := (avgOutputTokens / 1_000_000) * costOutputPrice
	totalCostPerDoc := inputCostPerDoc + outputCostPerDoc

	estimatedInputCost := inputCostPerDoc * float64(costDocCount)
	estimatedOutputCost := outputCostPerDoc * float64(costDocCount)
	estimatedTotalCost := totalCostPerDoc * float64(costDocCount)

	// Display results
	fmt.Printf("=== COST ESTIMATION ===\n")
	fmt.Printf("File: %s\n", filepath.Base(evalFile))
	fmt.Printf("Provider: %s\n", summary.Config.Provider)
	fmt.Printf("Model: %s\n", summary.Config.Model)
	fmt.Printf("\n")

	fmt.Printf("=== Token Usage Statistics ===\n")
	fmt.Printf("Documents analyzed: %d\n", docsWithTokens)
	fmt.Printf("Average input tokens per document: %.2f\n", avgInputTokens)
	fmt.Printf("Average output tokens per document: %.2f\n", avgOutputTokens)
	fmt.Printf("Average total tokens per document: %.2f\n", avgInputTokens+avgOutputTokens)
	fmt.Printf("\n")

	fmt.Printf("=== Pricing Configuration ===\n")
	fmt.Printf("Input token price: $%.2f per 1M tokens\n", costInputPrice)
	fmt.Printf("Output token price: $%.2f per 1M tokens\n", costOutputPrice)
	fmt.Printf("\n")

	fmt.Printf("=== Per Document Cost ===\n")
	fmt.Printf("Input cost: $%.6f\n", inputCostPerDoc)
	fmt.Printf("Output cost: $%.6f\n", outputCostPerDoc)
	fmt.Printf("Total cost: $%.6f\n", totalCostPerDoc)
	fmt.Printf("\n")

	fmt.Printf("=== Estimated Cost for %d Documents ===\n", costDocCount)
	fmt.Printf("Input cost: $%.2f\n", estimatedInputCost)
	fmt.Printf("Output cost: $%.2f\n", estimatedOutputCost)
	fmt.Printf("Total cost: $%.2f\n", estimatedTotalCost)

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
