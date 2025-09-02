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

	"github.com/lehigh-university-libraries/htr/pkg/azure"
	"github.com/lehigh-university-libraries/htr/pkg/gemini"
	"github.com/lehigh-university-libraries/htr/pkg/ollama"
	"github.com/lehigh-university-libraries/htr/pkg/openai"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"
)

type EvalConfig struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	Temperature float64 `json:"temperature"`
	CSVPath     string  `json:"csv_path"`
	TestRows    []int   `json:"rows"`
	Timestamp   string  `json:"timestamp"`
}

type EvalResult struct {
	Identifier            string  `json:"identifier"`
	ImagePath             string  `json:"image_path"`
	TranscriptPath        string  `json:"transcript_path"`
	Public                bool    `json:"public"`
	ProviderResponse      string  `json:"provider_response"`
	CharacterSimilarity   float64 `json:"character_similarity"`
	WordSimilarity        float64 `json:"word_similarity"`
	WordAccuracy          float64 `json:"word_accuracy"`
	WordErrorRate         float64 `json:"word_error_rate"`
	TotalWordsOriginal    int     `json:"total_words_original"`
	TotalWordsTranscribed int     `json:"total_words_transcribed"`
	CorrectWords          int     `json:"correct_words"`
	Substitutions         int     `json:"substitutions"`
	Deletions             int     `json:"deletions"`
	Insertions            int     `json:"insertions"`
}

type EvalSummary struct {
	Config  EvalConfig   `json:"config"`
	Results []EvalResult `json:"results"`
}

// Provider registry for managing all providers
var providerRegistry *providers.Registry

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Evaluate OCR performance using vision models",
	Long: `Evaluate OCR performance by comparing vision model outputs with ground truth transcripts.
	
You can either provide individual flags or use a previous evaluation config file.`,
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

var (
	evalProvider    string
	evalModel       string
	evalPrompt      string
	evalTemperature float64
	evalCSVPath     string
	evalConfigPath  string
	evalTemplate    string
	dir             string
	rows            []int
)

func init() {
	// Initialize provider registry
	providerRegistry = providers.NewRegistry()
	providerRegistry.Register(openai.New())
	providerRegistry.Register(azure.New())
	providerRegistry.Register(gemini.New())
	providerRegistry.Register(ollama.New())

	RootCmd.AddCommand(evalCmd)
	RootCmd.AddCommand(summaryCmd)

	evalCmd.Flags().StringVar(&evalProvider, "provider", "openai", "Provider to use: openai, azure, gemini, ollama")
	evalCmd.Flags().StringVarP(&evalModel, "model", "m", "gpt-4o", "Model to use")
	evalCmd.Flags().StringVarP(&evalPrompt, "prompt", "p", "", "Prompt to send to the provider")
	evalCmd.Flags().Float64VarP(&evalTemperature, "temperature", "t", 0.0, "Temperature for API")
	evalCmd.Flags().StringVarP(&evalCSVPath, "csv", "c", "", "Path to CSV file with evaluation data")
	evalCmd.Flags().StringVar(&evalConfigPath, "config", "", "Path to previous evaluation config file to rerun")
	evalCmd.Flags().StringVar(&evalTemplate, "template", "", "Custom JSON template file for API (optional)")
	evalCmd.Flags().StringVar(&dir, "dir", "./", "Prepend your CSV file paths with a directory")
	evalCmd.Flags().IntSliceVar(&rows, "rows", []int{}, "A list of row numbers to run the test on")

	evalCmd.MarkFlagsRequiredTogether("csv", "prompt")
	evalCmd.MarkFlagsMutuallyExclusive("csv", "config")
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
			Provider:    evalProvider,
			Model:       evalModel,
			Prompt:      evalPrompt,
			Temperature: evalTemperature,
			CSVPath:     evalCSVPath,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		}
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

	outputPath := filepath.Join(evalsDir, fmt.Sprintf("eval_%s.yaml", config.Timestamp))
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
		files, err := filepath.Glob(filepath.Join(evalsDir, "eval_*.yaml"))
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
			slog.Error("Error processing row", "row", i+1, "err", err)
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

	providerResponse, err := extractTextWithProvider(config, imagePath, imageBase64)
	if err != nil {
		return EvalResult{}, fmt.Errorf("provider API call failed: %w", err)
	}

	metrics := CalculateAccuracyMetrics(groundTruth, providerResponse)

	result := EvalResult{
		Identifier:            filepath.Base(imagePath),
		ImagePath:             imagePath,
		TranscriptPath:        transcriptPath,
		Public:                public,
		ProviderResponse:      providerResponse,
		CharacterSimilarity:   metrics.CharacterSimilarity,
		WordSimilarity:        metrics.WordSimilarity,
		WordAccuracy:          metrics.WordAccuracy,
		WordErrorRate:         metrics.WordErrorRate,
		TotalWordsOriginal:    metrics.TotalWordsOriginal,
		TotalWordsTranscribed: metrics.TotalWordsTranscribed,
		CorrectWords:          metrics.CorrectWords,
		Substitutions:         metrics.Substitutions,
		Deletions:             metrics.Deletions,
		Insertions:            metrics.Insertions,
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
func extractTextWithProvider(config EvalConfig, imagePath, imageBase64 string) (string, error) {
	// Get provider from registry
	provider, err := providerRegistry.Get(config.Provider)
	if err != nil {
		return "", fmt.Errorf("unsupported provider: %s", config.Provider)
	}

	// Convert EvalConfig to providers.Config
	providerConfig := providers.Config{
		Provider:    config.Provider,
		Model:       config.Model,
		Prompt:      config.Prompt,
		Temperature: config.Temperature,
	}

	// Validate configuration
	if err := provider.ValidateConfig(providerConfig); err != nil {
		return "", fmt.Errorf("invalid configuration for provider %s: %w", config.Provider, err)
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
	fmt.Printf("Word Similarity: %.3f\n", result.WordSimilarity)
	fmt.Printf("Word Accuracy: %.3f\n", result.WordAccuracy)
	fmt.Printf("Word Error Rate: %.3f\n", result.WordErrorRate)
	fmt.Printf("Total Words (Original): %d\n", result.TotalWordsOriginal)
	fmt.Printf("Total Words (Transcribed): %d\n", result.TotalWordsTranscribed)
	fmt.Printf("Correct Words: %d\n", result.CorrectWords)
	fmt.Printf("Substitutions: %d\n", result.Substitutions)
	fmt.Printf("Deletions: %d\n", result.Deletions)
	fmt.Printf("Insertions: %d\n", result.Insertions)
}

func printSummaryStats(results []EvalResult) {
	if len(results) == 0 {
		return
	}

	var totalCharSim, totalWordSim, totalWordAcc, totalWER float64

	for _, result := range results {
		totalCharSim += result.CharacterSimilarity
		totalWordSim += result.WordSimilarity
		totalWordAcc += result.WordAccuracy
		totalWER += result.WordErrorRate
	}

	count := float64(len(results))

	fmt.Printf("\n=== SUMMARY STATISTICS ===\n")
	fmt.Printf("Total Evaluations: %d\n", len(results))
	fmt.Printf("Average Character Similarity: %.3f\n", totalCharSim/count)
	fmt.Printf("Average Word Similarity: %.3f\n", totalWordSim/count)
	fmt.Printf("Average Word Accuracy: %.3f\n", totalWordAcc/count)
	fmt.Printf("Average Word Error Rate: %.3f\n", totalWER/count)
}

func normalizeText(text string) string {
	// Simple normalization - trim and convert to lowercase
	return strings.ToLower(strings.TrimSpace(text))
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

func CalculateAccuracyMetrics(original, transcribed string) EvalResult {
	origNorm := normalizeText(original)
	transNorm := normalizeText(transcribed)
	charSim := calculateSimilarity(origNorm, transNorm)
	origWords := strings.Fields(origNorm)
	transWords := strings.Fields(transNorm)
	wordSim := calculateSimilarity(strings.Join(origWords, " "), strings.Join(transWords, " "))
	wordAcc, correct, subs, dels, ins := calculateWordLevelMetrics(origWords, transWords)

	wer := 1.0 - wordAcc

	return EvalResult{
		CharacterSimilarity:   charSim,
		WordSimilarity:        wordSim,
		WordAccuracy:          wordAcc,
		WordErrorRate:         wer,
		TotalWordsOriginal:    len(origWords),
		TotalWordsTranscribed: len(transWords),
		CorrectWords:          correct,
		Substitutions:         subs,
		Deletions:             dels,
		Insertions:            ins,
	}
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
