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

func TestExternalEvalWithIgnorePatterns(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruth            string
		transcription          string
		ignorePatterns         []string
		expectedWordAccuracy   float64
		expectedCharSimilarity float64
		expectedIgnoredCount   int
		expectedCorrectWords   int
		description            string
	}{
		{
			name:                   "perfect match with unknown word ignored",
			groundTruth:            "The quick | fox jumps over the lazy dog",
			transcription:          "The quick brown fox jumps over the lazy dog",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   8,
			description:            "Unknown word 'brown' should be skipped, resulting in perfect match",
		},
		{
			name:                   "perfect match with unknown character ignored",
			groundTruth:            "John's d|te of birth is March 15th",
			transcription:          "John's date of birth is March 15th",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   7,
			description:            "Unknown character 'a' in 'date' should be skipped",
		},
		{
			name:                   "multiple ignore patterns",
			groundTruth:            "The | cat , jumped high",
			transcription:          "The quick cat suddenly jumped high",
			ignorePatterns:         []string{"|", ","},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   2,
			expectedCorrectWords:   4,
			description:            "Both '|' and ',' should be ignored",
		},
		{
			name:                   "ignore pattern but transcription has error",
			groundTruth:            "The | cat jumped over the fence",
			transcription:          "The quick cat jumped over the fense",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   0.833, // 5 correct out of 6 words (after removing "|" and "quick")
			expectedCharSimilarity: 0.967, // High similarity but not perfect due to 'fense'
			expectedIgnoredCount:   1,
			expectedCorrectWords:   5,
			description:            "Unknown word ignored, but 'fence' vs 'fense' is still an error",
		},
		{
			name:                   "realistic document with multiple unknowns",
			groundTruth:            "On this day the 15th of | in the year 1842 the following resolution was passed by the committee The chairman Mr J|hn Smith declared the motion carried with | votes in favor",
			transcription:          "On this day the 15th of March in the year 1842 the following resolution was passed by the committee The chairman Mr John Smith declared the motion carried with 12 votes in favor",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   3,
			expectedCorrectWords:   31,
			description:            "Realistic historical document with unknown month, character in name, and number (no punctuation)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original ignore patterns
			originalIgnorePatterns := evalExternalIgnorePatterns
			defer func() { evalExternalIgnorePatterns = originalIgnorePatterns }()

			// Set ignore patterns for this test
			evalExternalIgnorePatterns = tt.ignorePatterns

			// Create temporary files
			tmpDir, err := os.MkdirTemp("", "eval-external-ignore-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			gtPath := filepath.Join(tmpDir, "ground.txt")
			transPath := filepath.Join(tmpDir, "trans.txt")

			if err := os.WriteFile(gtPath, []byte(tt.groundTruth), 0644); err != nil {
				t.Fatalf("Failed to write ground truth file: %v", err)
			}
			if err := os.WriteFile(transPath, []byte(tt.transcription), 0644); err != nil {
				t.Fatalf("Failed to write transcription file: %v", err)
			}

			// Set evalExternalDir
			originalDir := evalExternalDir
			evalExternalDir = tmpDir
			defer func() { evalExternalDir = originalDir }()

			// Process the row
			row := []string{"ground.txt", "trans.txt"}
			result, err := processExternalEvalRow(row)
			if err != nil {
				t.Fatalf("processExternalEvalRow() error = %v", err)
			}

			// Check ignored count
			if result.IgnoredCharsCount != tt.expectedIgnoredCount {
				t.Errorf("IgnoredCharsCount = %d, want %d\n  description: %s",
					result.IgnoredCharsCount, tt.expectedIgnoredCount, tt.description)
			}

			// Check correct words
			if result.CorrectWords != tt.expectedCorrectWords {
				t.Errorf("CorrectWords = %d, want %d\n  description: %s",
					result.CorrectWords, tt.expectedCorrectWords, tt.description)
			}

			// Check word accuracy (with tolerance)
			if diff := result.WordAccuracy - tt.expectedWordAccuracy; diff > 0.01 || diff < -0.01 {
				t.Errorf("WordAccuracy = %.3f, want %.3f\n  description: %s",
					result.WordAccuracy, tt.expectedWordAccuracy, tt.description)
			}

			// Check character similarity (with tolerance)
			if diff := result.CharacterSimilarity - tt.expectedCharSimilarity; diff > 0.01 || diff < -0.01 {
				t.Errorf("CharacterSimilarity = %.3f, want %.3f\n  description: %s",
					result.CharacterSimilarity, tt.expectedCharSimilarity, tt.description)
			}
		})
	}
}
