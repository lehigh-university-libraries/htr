package metrics_test

import (
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/metrics"
)

func TestLevenshteinDistanceIsUnicodeAware(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"", "", 0},
		{"kitten", "sitting", 3},
		{"école", "ecole", 1},
		{"你好世界", "你好世", 1},
		{"مرحبا", "مرحبه", 1},
	}
	for _, test := range tests {
		if got := metrics.LevenshteinDistance(test.left, test.right); got != test.want {
			t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestEvaluateWordEditsAndIgnorePatterns(t *testing.T) {
	result := metrics.Evaluate(
		"The | cat d|te",
		"The quick cat date",
		metrics.Options{IgnorePatterns: []string{"|"}},
	)
	if result.WordErrorRate != 0 || result.CorrectWords != 3 || result.IgnoredCharsCount != 2 {
		t.Fatalf("Evaluate() = %+v, want an exact aligned result with two ignored markers", result)
	}
}

func TestEvaluateSingleLineAndUnicodeDenominator(t *testing.T) {
	result := metrics.Evaluate("école\n世界", "ecole 世界", metrics.Options{SingleLine: true})
	if result.CharacterDistance != 1 {
		t.Fatalf("character distance = %d, want 1", result.CharacterDistance)
	}
	if result.TotalWordsOriginal != 2 || result.WordErrorRate != 0.5 {
		t.Fatalf("word metrics = %+v, want one substitution across two words", result)
	}
}

func TestAlignWords(t *testing.T) {
	edits := metrics.AlignWords(
		[]string{"one", "two", "three"},
		[]string{"one", "too", "three", "four"},
	)
	if edits.Distance != 2 || edits.Correct != 2 || edits.Substitutions != 1 || edits.Insertions != 1 || edits.Deletions != 0 {
		t.Fatalf("AlignWords() = %+v", edits)
	}
}
