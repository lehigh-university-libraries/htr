package cmd

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ExternalEvalConfig stores configuration for evaluating external model results
type ExternalEvalConfig struct {
	ModelName string `json:"model_name"`
	CSVPath   string `json:"csv_path"`
	TestRows  []int  `json:"rows"`
	Timestamp string `json:"timestamp"`
}

var evalExternalCmd = &cobra.Command{
	Use:   "eval-external",
	Short: "Evaluate external model transcriptions against ground truth",
	Long: `Evaluate transcription results from external OCR/HTR models (like Loghi, Tesseract, etc.)
against ground truth transcripts.

This command expects a CSV file with 2 columns:
  transcript,transcription

Where:
  - transcript: path to ground truth transcript
  - transcription: path to external model's transcription output

Example:
  htr eval-external --csv loghi_results.csv --name loghi --dir ./fixtures`,
	RunE: runEvalExternal,
}

var (
	evalExternalCSVPath   string
	evalExternalModelName string
	evalExternalDir       string
	evalExternalRows      []int
)

func init() {
	RootCmd.AddCommand(evalExternalCmd)

	evalExternalCmd.Flags().StringVarP(&evalExternalCSVPath, "csv", "c", "", "Path to CSV file with external evaluation data (required)")
	evalExternalCmd.Flags().StringVarP(&evalExternalModelName, "name", "n", "", "Name of the external model (e.g., 'loghi', 'tesseract') (required)")
	evalExternalCmd.Flags().StringVar(&evalExternalDir, "dir", "./", "Prepend your CSV file paths with a directory")
	evalExternalCmd.Flags().IntSliceVar(&evalExternalRows, "rows", []int{}, "A list of row numbers to process")

	if err := evalExternalCmd.MarkFlagRequired("csv"); err != nil {
		panic(err)
	}
	if err := evalExternalCmd.MarkFlagRequired("name"); err != nil {
		panic(err)
	}
}

func runEvalExternal(cmd *cobra.Command, args []string) error {
	config := ExternalEvalConfig{
		ModelName: evalExternalModelName,
		CSVPath:   evalExternalCSVPath,
		TestRows:  evalExternalRows,
		Timestamp: time.Now().Format("2006-01-02_15-04-05"),
	}

	// Create evals directory if it doesn't exist
	evalsDir := "evals"
	if err := os.MkdirAll(evalsDir, 0755); err != nil {
		return fmt.Errorf("failed to create evals directory: %w", err)
	}

	// Process the external evaluation
	results, err := processExternalEval(config)
	if err != nil {
		return fmt.Errorf("external evaluation failed: %w", err)
	}

	// Create a summary that's compatible with existing eval format
	// We'll use an EvalConfig that indicates this is an external evaluation
	evalConfig := EvalConfig{
		Provider:    "external",
		Model:       config.ModelName,
		Prompt:      "Evaluated from external source",
		Temperature: 0.0,
		CSVPath:     config.CSVPath,
		TestRows:    config.TestRows,
		Timestamp:   config.Timestamp,
	}

	summary := EvalSummary{
		Config:  evalConfig,
		Results: results,
	}

	// Save results with model name
	m := strings.ReplaceAll(config.ModelName, ":", "_")
	outputPath := filepath.Join(evalsDir, fmt.Sprintf("%s.yaml", m))
	if err := saveEvalResults(summary, outputPath); err != nil {
		return fmt.Errorf("failed to save results: %w", err)
	}

	fmt.Printf("\nExternal evaluation completed. Results saved to: %s\n", outputPath)
	printSummaryStats(results)

	return nil
}

func processExternalEval(config ExternalEvalConfig) ([]EvalResult, error) {
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
	if len(records) > 0 && strings.EqualFold(records[0][0], "transcript") {
		dataRows = records[1:]
	}

	// If no specific rows specified, process all
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

		// Check for 2 columns: transcript, transcription
		if len(row) < 2 {
			slog.Warn("Insufficient columns (expected 2: transcript, transcription)", "row", i+1, "columns", len(row))
			continue
		}

		result, err := processExternalEvalRow(row)
		if err != nil {
			slog.Error("Error processing row", "row", i+1, "err", err)
			continue
		}

		results = append(results, result)
		printRowResult(result)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no rows were successfully processed")
	}

	return results, nil
}

func processExternalEvalRow(row []string) (EvalResult, error) {
	transcriptPath := filepath.Join(evalExternalDir, strings.TrimSpace(row[0]))
	transcriptionPath := filepath.Join(evalExternalDir, strings.TrimSpace(row[1]))

	// Read ground truth
	groundTruth, err := readTextFile(transcriptPath)
	if err != nil {
		return EvalResult{}, fmt.Errorf("failed to read ground truth transcript: %w", err)
	}

	// Read external model transcription
	externalTranscription, err := readTextFile(transcriptionPath)
	if err != nil {
		return EvalResult{}, fmt.Errorf("failed to read external transcription: %w", err)
	}

	// Calculate metrics
	metrics := CalculateAccuracyMetrics(groundTruth, externalTranscription)

	result := EvalResult{
		Identifier:            filepath.Base(transcriptPath),
		ImagePath:             "",
		TranscriptPath:        transcriptPath,
		Public:                false,
		ProviderResponse:      externalTranscription,
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
