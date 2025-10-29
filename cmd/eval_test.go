package cmd

import (
	"testing"
)

func TestApplyIgnorePatterns(t *testing.T) {
	tests := []struct {
		name            string
		groundTruth     string
		transcription   string
		ignorePatterns  []string
		expectedGT      string
		expectedTrans   string
		expectedIgnored int
		description     string
	}{
		{
			name:            "no ignore patterns",
			groundTruth:     "hello world",
			transcription:   "hello world",
			ignorePatterns:  []string{},
			expectedGT:      "hello world",
			expectedTrans:   "hello world",
			expectedIgnored: 0,
			description:     "When no ignore patterns are provided, text should remain unchanged",
		},
		{
			name:            "standalone pipe (unknown word)",
			groundTruth:     "hello | world",
			transcription:   "hello foo world",
			ignorePatterns:  []string{"|"},
			expectedGT:      "hello  world",
			expectedTrans:   "hello  world",
			expectedIgnored: 1,
			description:     "Pipe surrounded by spaces represents unknown word - skip 'foo' in transcription",
		},
		{
			name:            "pipe within word (unknown character)",
			groundTruth:     "hel|o",
			transcription:   "hello",
			ignorePatterns:  []string{"|"},
			expectedGT:      "helo",
			expectedTrans:   "helo",
			expectedIgnored: 1,
			description:     "Pipe within word represents unknown character - skip one char 'l' in transcription",
		},
		{
			name:            "multiple standalone pipes",
			groundTruth:     "the | cat | jumped",
			transcription:   "the quick cat suddenly jumped",
			ignorePatterns:  []string{"|"},
			expectedGT:      "the  cat  jumped",
			expectedTrans:   "the  cat  jumped",
			expectedIgnored: 2,
			description:     "Multiple unknown words - skip 'quick' and 'suddenly'",
		},
		{
			name:            "multiple pipes within word",
			groundTruth:     "h|l|o",
			transcription:   "hello",
			ignorePatterns:  []string{"|"},
			expectedGT:      "hlo",
			expectedTrans:   "hlo",
			expectedIgnored: 2,
			description:     "Multiple unknown characters in word - skip 'e' and 'l'",
		},
		{
			name:            "mixed standalone and within word",
			groundTruth:     "hel|o | world",
			transcription:   "hello foo world",
			ignorePatterns:  []string{"|"},
			expectedGT:      "helo  world",
			expectedTrans:   "helo  world",
			expectedIgnored: 2,
			description:     "Mix of character and word unknowns",
		},
		{
			name:            "pipe at start of text",
			groundTruth:     "| hello world",
			transcription:   "foo hello world",
			ignorePatterns:  []string{"|"},
			expectedGT:      " hello world",
			expectedTrans:   " hello world",
			expectedIgnored: 1,
			description:     "Unknown word at the beginning",
		},
		{
			name:            "pipe at end of text",
			groundTruth:     "hello world |",
			transcription:   "hello world foo",
			ignorePatterns:  []string{"|"},
			expectedGT:      "hello world ",
			expectedTrans:   "hello world ",
			expectedIgnored: 1,
			description:     "Unknown word at the end",
		},
		{
			name:            "multiple ignore patterns",
			groundTruth:     "hello | world , test",
			transcription:   "hello foo world bar test",
			ignorePatterns:  []string{"|", ","},
			expectedGT:      "hello  world  test",
			expectedTrans:   "hello  world  test",
			expectedIgnored: 2,
			description:     "Multiple different ignore patterns (pipe and comma)",
		},
		{
			name:            "comma as ignore pattern",
			groundTruth:     "hello , world",
			transcription:   "hello something world",
			ignorePatterns:  []string{","},
			expectedGT:      "hello  world",
			expectedTrans:   "hello  world",
			expectedIgnored: 1,
			description:     "Using comma as ignore pattern",
		},
		{
			name:            "multi-character ignore pattern",
			groundTruth:     "hello [?] world",
			transcription:   "hello unknown world",
			ignorePatterns:  []string{"[?]"},
			expectedGT:      "hello  world",
			expectedTrans:   "hello  world",
			expectedIgnored: 3,
			description:     "Multi-character ignore pattern as standalone word",
		},
		{
			name:            "consecutive pipes in word",
			groundTruth:     "h||o",
			transcription:   "helo",
			ignorePatterns:  []string{"|"},
			expectedGT:      "ho",
			expectedTrans:   "ho",
			expectedIgnored: 2,
			description:     "Consecutive unknown characters - each position in GT aligns with Trans position",
		},
		{
			name:            "real world example 1",
			groundTruth:     "The quick | fox jumps",
			transcription:   "The quick brown fox jumps",
			ignorePatterns:  []string{"|"},
			expectedGT:      "The quick  fox jumps",
			expectedTrans:   "The quick  fox jumps",
			expectedIgnored: 1,
			description:     "Unknown adjective before 'fox'",
		},
		{
			name:            "real world example 2",
			groundTruth:     "John's d|te of birth",
			transcription:   "John's date of birth",
			ignorePatterns:  []string{"|"},
			expectedGT:      "John's dte of birth",
			expectedTrans:   "John's dte of birth",
			expectedIgnored: 1,
			description:     "Unknown character in word 'date'",
		},
		{
			name:            "transcription shorter than expected",
			groundTruth:     "hello | world | test",
			transcription:   "hello foo",
			ignorePatterns:  []string{"|"},
			expectedGT:      "hello  world  test",
			expectedTrans:   "hello ",
			expectedIgnored: 2,
			description:     "Transcription ends before all words processed",
		},
		{
			name:            "transcription longer than expected",
			groundTruth:     "hello world",
			transcription:   "hello world extra words here",
			ignorePatterns:  []string{},
			expectedGT:      "hello world",
			expectedTrans:   "hello world extra words here",
			expectedIgnored: 0,
			description:     "No ignore patterns - function returns inputs unchanged",
		},
		{
			name:            "only ignore pattern",
			groundTruth:     "|",
			transcription:   "something",
			ignorePatterns:  []string{"|"},
			expectedGT:      "",
			expectedTrans:   "",
			expectedIgnored: 1,
			description:     "Ground truth contains only an unknown word",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGT, gotTrans, gotIgnored := applyIgnorePatterns(tt.groundTruth, tt.transcription, tt.ignorePatterns)

			if gotGT != tt.expectedGT {
				t.Errorf("Ground Truth mismatch:\n  got:  %q\n  want: %q\n  description: %s",
					gotGT, tt.expectedGT, tt.description)
			}

			if gotTrans != tt.expectedTrans {
				t.Errorf("Transcription mismatch:\n  got:  %q\n  want: %q\n  description: %s",
					gotTrans, tt.expectedTrans, tt.description)
			}

			if gotIgnored != tt.expectedIgnored {
				t.Errorf("Ignored count mismatch:\n  got:  %d\n  want: %d\n  description: %s",
					gotIgnored, tt.expectedIgnored, tt.description)
			}
		})
	}
}

func TestCalculateAccuracyMetricsWithIgnore(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruth            string
		transcription          string
		ignorePatterns         []string
		expectedWordAccuracy   float64
		expectedIgnoredCount   int
		expectedCorrectWords   int
		expectedTotalWordsOrig int
		description            string
	}{
		{
			name:                   "no ignore patterns - perfect match",
			groundTruth:            "hello world",
			transcription:          "hello world",
			ignorePatterns:         []string{},
			expectedWordAccuracy:   1.0,
			expectedIgnoredCount:   0,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "Perfect match without ignore patterns",
		},
		{
			name:                   "ignore pattern - standalone word",
			groundTruth:            "hello | world",
			transcription:          "hello unknown world",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "After removing unknown word, should be perfect match",
		},
		{
			name:                   "ignore pattern within word",
			groundTruth:            "hel|o",
			transcription:          "hello",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   1.0,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   1,
			expectedTotalWordsOrig: 1,
			description:            "After removing unknown character, 'helo' should match 'helo'",
		},
		{
			name:                   "multiple ignore patterns",
			groundTruth:            "the | cat , jumped",
			transcription:          "the quick cat suddenly jumped",
			ignorePatterns:         []string{"|", ","},
			expectedWordAccuracy:   1.0,
			expectedIgnoredCount:   2,
			expectedCorrectWords:   3,
			expectedTotalWordsOrig: 3,
			description:            "Multiple unknown words removed, result should be perfect",
		},
		{
			name:                   "ignore pattern but transcription error",
			groundTruth:            "hello | world",
			transcription:          "hello unknown warld",
			ignorePatterns:         []string{"|"},
			expectedWordAccuracy:   0.5,
			expectedIgnoredCount:   1,
			expectedCorrectWords:   1,
			expectedTotalWordsOrig: 2,
			description:            "Unknown word ignored, but 'world' vs 'warld' is still an error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateAccuracyMetrics(tt.groundTruth, tt.transcription, tt.ignorePatterns, false)

			if result.IgnoredCharsCount != tt.expectedIgnoredCount {
				t.Errorf("IgnoredCharsCount = %d, want %d\n  description: %s",
					result.IgnoredCharsCount, tt.expectedIgnoredCount, tt.description)
			}

			if result.CorrectWords != tt.expectedCorrectWords {
				t.Errorf("CorrectWords = %d, want %d\n  description: %s",
					result.CorrectWords, tt.expectedCorrectWords, tt.description)
			}

			if result.TotalWordsOriginal != tt.expectedTotalWordsOrig {
				t.Errorf("TotalWordsOriginal = %d, want %d\n  description: %s",
					result.TotalWordsOriginal, tt.expectedTotalWordsOrig, tt.description)
			}

			// Word accuracy with tolerance for floating point
			if diff := result.WordAccuracy - tt.expectedWordAccuracy; diff > 0.01 || diff < -0.01 {
				t.Errorf("WordAccuracy = %.3f, want %.3f\n  description: %s",
					result.WordAccuracy, tt.expectedWordAccuracy, tt.description)
			}
		})
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "identical strings",
			s1:       "hello",
			s2:       "hello",
			expected: 0,
		},
		{
			name:     "one substitution",
			s1:       "hello",
			s2:       "hallo",
			expected: 1,
		},
		{
			name:     "one insertion",
			s1:       "hello",
			s2:       "helloo",
			expected: 1,
		},
		{
			name:     "one deletion",
			s1:       "hello",
			s2:       "hell",
			expected: 1,
		},
		{
			name:     "empty strings",
			s1:       "",
			s2:       "",
			expected: 0,
		},
		{
			name:     "one empty string",
			s1:       "hello",
			s2:       "",
			expected: 5,
		},
		{
			name:     "completely different",
			s1:       "abc",
			s2:       "xyz",
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := levenshteinDistance(tt.s1, tt.s2)
			if got != tt.expected {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d",
					tt.s1, tt.s2, got, tt.expected)
			}
		})
	}
}

func TestCalculateSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected float64
	}{
		{
			name:     "identical strings",
			s1:       "hello",
			s2:       "hello",
			expected: 1.0,
		},
		{
			name:     "completely different",
			s1:       "abc",
			s2:       "xyz",
			expected: 0.0,
		},
		{
			name:     "one char different",
			s1:       "hello",
			s2:       "hallo",
			expected: 0.8,
		},
		{
			name:     "empty strings",
			s1:       "",
			s2:       "",
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSimilarity(tt.s1, tt.s2)
			if diff := got - tt.expected; diff > 0.01 || diff < -0.01 {
				t.Errorf("calculateSimilarity(%q, %q) = %.3f, want %.3f",
					tt.s1, tt.s2, got, tt.expected)
			}
		})
	}
}

func TestCalculateAccuracyMetricsWithSingleLine(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruth            string
		transcription          string
		singleLine             bool
		expectedWordAccuracy   float64
		expectedCorrectWords   int
		expectedTotalWordsOrig int
		description            string
	}{
		{
			name:                   "multi-line ground truth without single-line",
			groundTruth:            "hello\nworld",
			transcription:          "hello world",
			singleLine:             false,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "Newline in ground truth is treated as space without single-line",
		},
		{
			name:                   "multi-line ground truth with single-line",
			groundTruth:            "hello\nworld\ntest",
			transcription:          "hello world test",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   3,
			expectedTotalWordsOrig: 3,
			description:            "Newlines removed and converted to spaces with single-line",
		},
		{
			name:                   "carriage return handling with single-line",
			groundTruth:            "hello\r\nworld",
			transcription:          "hello world",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "Both \\r and \\n removed with single-line",
		},
		{
			name:                   "complex multi-line document with single-line",
			groundTruth:            "Line one\nLine two\nLine three",
			transcription:          "Line one Line two Line three",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   6,
			expectedTotalWordsOrig: 6,
			description:            "Multi-line document converted to single line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateAccuracyMetrics(tt.groundTruth, tt.transcription, []string{}, tt.singleLine)

			if result.CorrectWords != tt.expectedCorrectWords {
				t.Errorf("CorrectWords = %d, want %d\n  description: %s",
					result.CorrectWords, tt.expectedCorrectWords, tt.description)
			}

			if result.TotalWordsOriginal != tt.expectedTotalWordsOrig {
				t.Errorf("TotalWordsOriginal = %d, want %d\n  description: %s",
					result.TotalWordsOriginal, tt.expectedTotalWordsOrig, tt.description)
			}

			if diff := result.WordAccuracy - tt.expectedWordAccuracy; diff > 0.01 || diff < -0.01 {
				t.Errorf("WordAccuracy = %.3f, want %.3f\n  description: %s",
					result.WordAccuracy, tt.expectedWordAccuracy, tt.description)
			}
		})
	}
}

func TestNormalizeSpaces(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no extra spaces",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "double spaces",
			input:    "hello  world",
			expected: "hello world",
		},
		{
			name:     "multiple spaces",
			input:    "hello     world",
			expected: "hello world",
		},
		{
			name:     "spaces at multiple locations",
			input:    "hello   world  test   example",
			expected: "hello world test example",
		},
		{
			name:     "leading space preserved",
			input:    " hello world",
			expected: " hello world",
		},
		{
			name:     "trailing space preserved",
			input:    "hello world ",
			expected: "hello world ",
		},
		{
			name:     "only spaces",
			input:    "     ",
			expected: " ",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeSpaces(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeSpaces(%q) = %q, want %q",
					tt.input, got, tt.expected)
			}
		})
	}
}

func TestCalculateAccuracyMetricsWithSingleLineNormalization(t *testing.T) {
	tests := []struct {
		name                   string
		groundTruth            string
		transcription          string
		singleLine             bool
		expectedWordAccuracy   float64
		expectedCorrectWords   int
		expectedTotalWordsOrig int
		description            string
	}{
		{
			name:                   "newlines create double spaces - normalized",
			groundTruth:            "hello\n\nworld",
			transcription:          "hello world",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "Multiple newlines create multiple spaces, normalized to single space",
		},
		{
			name:                   "tabs replaced with spaces",
			groundTruth:            "hello\tworld",
			transcription:          "hello world",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   2,
			expectedTotalWordsOrig: 2,
			description:            "Tabs replaced with spaces and normalized",
		},
		{
			name:                   "mixed whitespace normalized",
			groundTruth:            "hello\n\t\nworld\t\ttest",
			transcription:          "hello world test",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   3,
			expectedTotalWordsOrig: 3,
			description:            "Mixed newlines and tabs all normalized to single spaces",
		},
		{
			name:                   "complex document with various whitespace",
			groundTruth:            "Line one\n\nLine two\t\tLine three",
			transcription:          "Line one Line two Line three",
			singleLine:             true,
			expectedWordAccuracy:   1.0,
			expectedCorrectWords:   6,
			expectedTotalWordsOrig: 6,
			description:            "Multi-line document with various whitespace types normalized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateAccuracyMetrics(tt.groundTruth, tt.transcription, []string{}, tt.singleLine)

			if result.CorrectWords != tt.expectedCorrectWords {
				t.Errorf("CorrectWords = %d, want %d\n  description: %s",
					result.CorrectWords, tt.expectedCorrectWords, tt.description)
			}

			if result.TotalWordsOriginal != tt.expectedTotalWordsOrig {
				t.Errorf("TotalWordsOriginal = %d, want %d\n  description: %s",
					result.TotalWordsOriginal, tt.expectedTotalWordsOrig, tt.description)
			}

			if diff := result.WordAccuracy - tt.expectedWordAccuracy; diff > 0.01 || diff < -0.01 {
				t.Errorf("WordAccuracy = %.3f, want %.3f\n  description: %s",
					result.WordAccuracy, tt.expectedWordAccuracy, tt.description)
			}
		})
	}
}

func TestPageCostCalculation(t *testing.T) {
	tests := []struct {
		name              string
		results           []EvalResult
		inputPrice        float64
		outputPrice       float64
		expectedAvgInput  float64
		expectedAvgOutput float64
		expectedPageCost  float64
		description       string
	}{
		{
			name: "basic cost calculation",
			results: []EvalResult{
				{InputTokens: 1000, OutputTokens: 500},
				{InputTokens: 2000, OutputTokens: 1000},
				{InputTokens: 1500, OutputTokens: 750},
			},
			inputPrice:        2.50,     // $2.50 per 1M tokens
			outputPrice:       10.0,     // $10.00 per 1M tokens
			expectedAvgInput:  1500,     // (1000 + 2000 + 1500) / 3
			expectedAvgOutput: 750,      // (500 + 1000 + 750) / 3
			expectedPageCost:  0.011250, // (1500/1M * 2.50) + (750/1M * 10.0) = 0.00375 + 0.0075 = 0.01125
			description:       "Calculate average tokens and page cost with standard pricing",
		},
		{
			name: "zero tokens",
			results: []EvalResult{
				{InputTokens: 0, OutputTokens: 0},
				{InputTokens: 0, OutputTokens: 0},
			},
			inputPrice:        2.50,
			outputPrice:       10.0,
			expectedAvgInput:  0,
			expectedAvgOutput: 0,
			expectedPageCost:  0.0,
			description:       "No tokens results in zero cost",
		},
		{
			name: "only input tokens",
			results: []EvalResult{
				{InputTokens: 1000, OutputTokens: 0},
				{InputTokens: 2000, OutputTokens: 0},
			},
			inputPrice:        5.0,
			outputPrice:       10.0,
			expectedAvgInput:  1500,
			expectedAvgOutput: 0,
			expectedPageCost:  0.0075, // (1500/1M * 5.0)
			description:       "Cost calculation with only input tokens",
		},
		{
			name: "only output tokens",
			results: []EvalResult{
				{InputTokens: 0, OutputTokens: 1000},
				{InputTokens: 0, OutputTokens: 2000},
			},
			inputPrice:        2.50,
			outputPrice:       15.0,
			expectedAvgInput:  0,
			expectedAvgOutput: 1500,
			expectedPageCost:  0.0225, // (1500/1M * 15.0)
			description:       "Cost calculation with only output tokens",
		},
		{
			name: "zero input price",
			results: []EvalResult{
				{InputTokens: 1000, OutputTokens: 500},
				{InputTokens: 2000, OutputTokens: 1000},
			},
			inputPrice:        0.0,
			outputPrice:       10.0,
			expectedAvgInput:  1500,
			expectedAvgOutput: 750,
			expectedPageCost:  0.0075, // (750/1M * 10.0)
			description:       "Cost calculation with zero input price",
		},
		{
			name: "zero output price",
			results: []EvalResult{
				{InputTokens: 1000, OutputTokens: 500},
				{InputTokens: 2000, OutputTokens: 1000},
			},
			inputPrice:        2.50,
			outputPrice:       0.0,
			expectedAvgInput:  1500,
			expectedAvgOutput: 750,
			expectedPageCost:  0.00375, // (1500/1M * 2.50)
			description:       "Cost calculation with zero output price",
		},
		{
			name: "single evaluation",
			results: []EvalResult{
				{InputTokens: 5000, OutputTokens: 2500},
			},
			inputPrice:        3.0,
			outputPrice:       12.0,
			expectedAvgInput:  5000,
			expectedAvgOutput: 2500,
			expectedPageCost:  0.045, // (5000/1M * 3.0) + (2500/1M * 12.0) = 0.015 + 0.03 = 0.045
			description:       "Cost calculation with single evaluation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate totals
			var totalInputTokens, totalOutputTokens int
			for _, result := range tt.results {
				totalInputTokens += result.InputTokens
				totalOutputTokens += result.OutputTokens
			}

			count := float64(len(tt.results))
			avgInputTokens := float64(totalInputTokens) / count
			avgOutputTokens := float64(totalOutputTokens) / count

			// Calculate page cost
			pageCost := 0.0
			if tt.inputPrice > 0 || tt.outputPrice > 0 {
				inputCost := (avgInputTokens / 1_000_000) * tt.inputPrice
				outputCost := (avgOutputTokens / 1_000_000) * tt.outputPrice
				pageCost = inputCost + outputCost
			}

			// Verify average input tokens
			if diff := avgInputTokens - tt.expectedAvgInput; diff > 0.01 || diff < -0.01 {
				t.Errorf("AvgInputTokens = %.2f, want %.2f\n  description: %s",
					avgInputTokens, tt.expectedAvgInput, tt.description)
			}

			// Verify average output tokens
			if diff := avgOutputTokens - tt.expectedAvgOutput; diff > 0.01 || diff < -0.01 {
				t.Errorf("AvgOutputTokens = %.2f, want %.2f\n  description: %s",
					avgOutputTokens, tt.expectedAvgOutput, tt.description)
			}

			// Verify page cost
			if diff := pageCost - tt.expectedPageCost; diff > 0.000001 || diff < -0.000001 {
				t.Errorf("PageCost = %.6f, want %.6f\n  description: %s",
					pageCost, tt.expectedPageCost, tt.description)
			}
		})
	}
}
