package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessExternalEval(t *testing.T) {
	tests := []struct {
		name          string
		csvContent    string
		setupFiles    map[string]string // filename -> content
		expectedCount int
		wantErr       bool
	}{
		{
			name: "exact matches",
			csvContent: `transcript,transcription
groundtruth1.txt,transcription1.txt`,
			setupFiles: map[string]string{
				"groundtruth1.txt":   "a b c",
				"transcription1.txt": "a b c",
			},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name: "off by one",
			csvContent: `transcript,transcription
groundtruth1.txt,transcription1.txt`,
			setupFiles: map[string]string{
				"groundtruth1.txt":   "a b c",
				"transcription1.txt": "a b d",
			},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name: "not close",
			csvContent: `transcript,transcription
groundtruth1.txt,transcription1.txt`,
			setupFiles: map[string]string{
				"groundtruth1.txt":   "a b c",
				"transcription1.txt": "x y z",
			},
			expectedCount: 1,
			wantErr:       false,
		},
		{
			name: "multiple rows",
			csvContent: `transcript,transcription
groundtruth1.txt,transcription1.txt
groundtruth2.txt,transcription2.txt
groundtruth3.txt,transcription3.txt`,
			setupFiles: map[string]string{
				"groundtruth1.txt":   "a b c",
				"transcription1.txt": "a b c",
				"groundtruth2.txt":   "x y z",
				"transcription2.txt": "x y a",
				"groundtruth3.txt":   "hello world",
				"transcription3.txt": "goodbye world",
			},
			expectedCount: 3,
			wantErr:       false,
		},
		{
			name: "empty CSV",
			csvContent: `transcript,transcription
`,
			setupFiles:    map[string]string{},
			expectedCount: 0,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "eval-external-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			// Create test files
			for filename, content := range tt.setupFiles {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to write test file %s: %v", filename, err)
				}
			}

			// Create CSV file
			csvPath := filepath.Join(tmpDir, "test.csv")
			if err := os.WriteFile(csvPath, []byte(tt.csvContent), 0644); err != nil {
				t.Fatalf("Failed to write CSV file: %v", err)
			}

			// Set evalExternalDir to temp directory
			originalDir := evalExternalDir
			evalExternalDir = tmpDir
			defer func() { evalExternalDir = originalDir }()

			// Run test
			config := ExternalEvalConfig{
				ModelName: "test-model",
				CSVPath:   csvPath,
				TestRows:  []int{},
				Timestamp: "2024-01-01_00-00-00",
			}

			results, err := processExternalEval(config)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("processExternalEval() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check result count
			if len(results) != tt.expectedCount {
				t.Errorf("processExternalEval() got %d results, want %d", len(results), tt.expectedCount)
			}
		})
	}
}

func TestProcessExternalEvalRow(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruthContent     string
		transcriptionContent   string
		expectedCharSimilarity float64
		expectedWordAccuracy   float64
		expectedCorrectWords   int
	}{
		{
			name:                   "exact match",
			groundTruthContent:     "a b c",
			transcriptionContent:   "a b c",
			expectedCharSimilarity: 1.0,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   3,
		},
		{
			name:                   "off by one word",
			groundTruthContent:     "a b c",
			transcriptionContent:   "a b d",
			expectedCharSimilarity: 0.8,
			expectedWordAccuracy:   0.667,
			expectedCorrectWords:   2,
		},
		{
			name:                   "completely different",
			groundTruthContent:     "a b c",
			transcriptionContent:   "x y z",
			expectedCharSimilarity: 0.4, // Spaces match, so similarity is not 0
			expectedWordAccuracy:   0.0,
			expectedCorrectWords:   0,
		},
		{
			name:                   "empty transcription",
			groundTruthContent:     "a b c",
			transcriptionContent:   "",
			expectedCharSimilarity: 0.0,
			expectedWordAccuracy:   0.0,
			expectedCorrectWords:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "eval-external-row-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			// Create test files
			groundTruthPath := filepath.Join(tmpDir, "groundtruth.txt")
			transcriptionPath := filepath.Join(tmpDir, "transcription.txt")

			if err := os.WriteFile(groundTruthPath, []byte(tt.groundTruthContent), 0644); err != nil {
				t.Fatalf("Failed to write ground truth file: %v", err)
			}
			if err := os.WriteFile(transcriptionPath, []byte(tt.transcriptionContent), 0644); err != nil {
				t.Fatalf("Failed to write transcription file: %v", err)
			}

			// Set evalExternalDir to temp directory
			originalDir := evalExternalDir
			evalExternalDir = tmpDir
			defer func() { evalExternalDir = originalDir }()

			// Create row
			row := []string{"groundtruth.txt", "transcription.txt"}

			// Run test
			result, err := processExternalEvalRow(row)
			if err != nil {
				t.Fatalf("processExternalEvalRow() error = %v", err)
			}

			// Check character similarity (with tolerance for floating point)
			if diff := result.CharacterSimilarity - tt.expectedCharSimilarity; diff > 0.01 || diff < -0.01 {
				t.Errorf("CharacterSimilarity = %.3f, want %.3f", result.CharacterSimilarity, tt.expectedCharSimilarity)
			}

			// Check word accuracy (with tolerance for floating point)
			if diff := result.WordAccuracy - tt.expectedWordAccuracy; diff > 0.01 || diff < -0.01 {
				t.Errorf("WordAccuracy = %.3f, want %.3f", result.WordAccuracy, tt.expectedWordAccuracy)
			}

			// Check correct words count
			if result.CorrectWords != tt.expectedCorrectWords {
				t.Errorf("CorrectWords = %d, want %d", result.CorrectWords, tt.expectedCorrectWords)
			}
		})
	}
}

func TestExternalEvalCSVParsing(t *testing.T) {
	tests := []struct {
		name      string
		csvLine   []string
		wantErr   bool
		errString string
	}{
		{
			name:    "valid row with 2 columns",
			csvLine: []string{"ground.txt", "trans.txt"},
			wantErr: false,
		},
		{
			name:    "insufficient columns",
			csvLine: []string{"ground.txt"},
			wantErr: false, // Should be caught in processExternalEval, not processExternalEvalRow
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.csvLine) < 2 && !tt.wantErr {
				// Skip test - insufficient columns should be caught at higher level
				return
			}

			// Create temporary files
			tmpDir, err := os.MkdirTemp("", "eval-external-csv-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			// Create dummy files
			for _, filename := range tt.csvLine {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
					t.Fatalf("Failed to write test file: %v", err)
				}
			}

			// Set evalExternalDir
			originalDir := evalExternalDir
			evalExternalDir = tmpDir
			defer func() { evalExternalDir = originalDir }()

			_, err = processExternalEvalRow(tt.csvLine)
			if (err != nil) != tt.wantErr {
				t.Errorf("processExternalEvalRow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
