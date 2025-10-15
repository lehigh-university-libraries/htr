package cmd

import (
	"testing"
)

func TestExternalEval(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruthFile        string
		transcriptionFile      string
		ignorePatterns         []string
		expectedWordAccuracy   float64
		expectedCharSimilarity float64
		expectedIgnoredCount   int
		expectedCorrectWords   int
		description            string
	}{
		// Basic accuracy tests without ignore patterns
		{
			name:                   "exact match - abc",
			groundTruthFile:        "ground-truth/simple-abc.txt",
			transcriptionFile:      "external/exact-match-abc.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   3,
			description:            "Perfect match: 'a b c' vs 'a b c'",
		},
		{
			name:                   "exact match - xyz",
			groundTruthFile:        "ground-truth/simple-xyz.txt",
			transcriptionFile:      "external/exact-match-xyz.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   3,
			description:            "Perfect match: 'x y z' vs 'x y z'",
		},
		{
			name:                   "exact match - hello world",
			groundTruthFile:        "ground-truth/simple-hello-world.txt",
			transcriptionFile:      "external/exact-match-hello-world.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   3,
			description:            "Perfect match: 'hello world test' vs 'hello world test'",
		},
		{
			name:                   "off by one word - abc",
			groundTruthFile:        "ground-truth/simple-abc.txt",
			transcriptionFile:      "external/off-by-one-abc.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   0.667,
			expectedCharSimilarity: 0.8,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   2,
			description:            "One word different: 'a b c' vs 'a b d'",
		},
		{
			name:                   "off by one word - xyz",
			groundTruthFile:        "ground-truth/simple-xyz.txt",
			transcriptionFile:      "external/off-by-one-xyz.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   0.667,
			expectedCharSimilarity: 0.8,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   2,
			description:            "One word different: 'x y z' vs 'x y a'",
		},
		{
			name:                   "not close - abc",
			groundTruthFile:        "ground-truth/simple-abc.txt",
			transcriptionFile:      "external/not-close-abc.txt",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   0.0,
			expectedCharSimilarity: 0.273,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   0,
			description:            "Completely different: 'a b c' vs 'foo bar baz'",
		},
		// Tests with ignore patterns
		{
			name:                   "perfect match with unknown word ignored",
			groundTruthFile:        "ground-truth/unknown-word.txt",
			transcriptionFile:      "external/unknown-word.txt",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   8,
			description:            "Unknown word 'brown' should be skipped, resulting in perfect match",
		},
		{
			name:                   "perfect match with unknown character ignored",
			groundTruthFile:        "ground-truth/unknown-char.txt",
			transcriptionFile:      "external/unknown-char.txt",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   7,
			description:            "Unknown character 'a' in 'date' should be skipped",
		},
		{
			name:                   "multiple unknown words",
			groundTruthFile:        "ground-truth/multiple-unknown.txt",
			transcriptionFile:      "external/multiple-unknown.txt",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedCharSimilarity: 1.0,
			expectedIgnoredCount:   2,
			expectedCorrectWords:   4,
			description:            "Multiple '|' patterns should be ignored",
		},
		{
			name:                   "ignore pattern but transcription has error",
			groundTruthFile:        "ground-truth/partial-error.txt",
			transcriptionFile:      "external/partial-error.txt",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   0.833,
			expectedCharSimilarity: 0.967,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   5,
			description:            "Unknown word ignored, but 'fence' vs 'fense' is still an error",
		},
		{
			name:                   "realistic document with multiple unknowns",
			groundTruthFile:        "ground-truth/realistic-document.txt",
			transcriptionFile:      "external/realistic-document.txt",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   0.906,
			expectedCharSimilarity: 0.944,
			expectedIgnoredCount:   3,
			expectedCorrectWords:   29,
			description:            "Realistic historical document with punctuation affecting alignment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original ignore patterns
			originalIgnorePatterns := evalExternalIgnorePatterns
			defer func() { evalExternalIgnorePatterns = originalIgnorePatterns }()

			// Set ignore patterns for this test
			evalExternalIgnorePatterns = tt.ignorePatterns

			// Set evalExternalDir to fixtures directory (relative to project root)
			originalDir := evalExternalDir
			evalExternalDir = "../fixtures"
			defer func() { evalExternalDir = originalDir }()

			// Process the row
			row := []string{tt.groundTruthFile, tt.transcriptionFile}
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
