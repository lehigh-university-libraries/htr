// Package metrics provides reusable, Unicode-aware OCR and HTR evaluation
// metrics. It is intentionally independent of the htr command so services,
// benchmarks, and applications use the same definitions.
package metrics

import (
	"strings"
	"unicode"
)

// Options controls transformations applied before evaluating a transcription.
type Options struct {
	// IgnorePatterns mark unknown ground-truth characters or words. A pattern
	// surrounded by whitespace skips one hypothesis word; an inline pattern
	// skips one hypothesis rune.
	IgnorePatterns []string
	// SingleLine maps CR, LF, and tab characters to spaces and collapses runs
	// of ASCII spaces before calculating character metrics.
	SingleLine bool
}

// Result contains character- and word-level edit metrics.
type Result struct {
	CharacterDistance     int
	CharacterSimilarity   float64
	CharacterAccuracy     float64
	WordDistance          int
	WordSimilarity        float64
	WordAccuracy          float64
	WordErrorRate         float64
	TotalWordsOriginal    int
	TotalWordsTranscribed int
	CorrectWords          int
	Substitutions         int
	Deletions             int
	Insertions            int
	IgnoredCharsCount     int
}

// WordEdits contains the edit alignment for two token sequences.
type WordEdits struct {
	Distance      int
	Correct       int
	Substitutions int
	Deletions     int
	Insertions    int
}

// Evaluate compares a ground-truth string with a transcription.
func Evaluate(original, transcribed string, options Options) Result {
	if options.SingleLine {
		original = NormalizeSingleLine(original)
		transcribed = NormalizeSingleLine(transcribed)
	}
	original, transcribed, ignored := ApplyIgnorePatterns(original, transcribed, options.IgnorePatterns)

	characterDistance := LevenshteinDistance(original, transcribed)
	originalRunes := len([]rune(original))
	characterAccuracy := 1.0
	if originalRunes > 0 {
		characterAccuracy = 1.0 - float64(characterDistance)/float64(originalRunes)
	}

	originalWords := strings.Fields(original)
	transcribedWords := strings.Fields(transcribed)
	wordEdits := AlignWords(originalWords, transcribedWords)
	wordErrorRate := 0.0
	if len(originalWords) > 0 {
		wordErrorRate = float64(wordEdits.Distance) / float64(len(originalWords))
	}

	return Result{
		CharacterDistance:     characterDistance,
		CharacterSimilarity:   Similarity(original, transcribed),
		CharacterAccuracy:     characterAccuracy,
		WordDistance:          wordEdits.Distance,
		WordSimilarity:        WordSimilarity(originalWords, transcribedWords),
		WordAccuracy:          1.0 - wordErrorRate,
		WordErrorRate:         wordErrorRate,
		TotalWordsOriginal:    len(originalWords),
		TotalWordsTranscribed: len(transcribedWords),
		CorrectWords:          wordEdits.Correct,
		Substitutions:         wordEdits.Substitutions,
		Deletions:             wordEdits.Deletions,
		Insertions:            wordEdits.Insertions,
		IgnoredCharsCount:     ignored,
	}
}

// LevenshteinDistance returns the Unicode code-point edit distance between two
// strings using O(min(m,n)) memory.
func LevenshteinDistance(left, right string) int {
	return sequenceDistance([]rune(left), []rune(right))
}

// WordLevenshteinDistance returns the edit distance between token sequences.
func WordLevenshteinDistance(left, right []string) int {
	return sequenceDistance(left, right)
}

// Similarity returns one minus the character distance divided by the longer
// Unicode code-point length. Two empty strings have similarity 1.
func Similarity(left, right string) float64 {
	maximum := max(len([]rune(left)), len([]rune(right)))
	if maximum == 0 {
		return 1
	}
	return 1 - float64(LevenshteinDistance(left, right))/float64(maximum)
}

// WordSimilarity returns one minus the word distance divided by the longer
// token sequence. Two empty sequences have similarity 1.
func WordSimilarity(left, right []string) float64 {
	maximum := max(len(left), len(right))
	if maximum == 0 {
		return 1
	}
	return 1 - float64(WordLevenshteinDistance(left, right))/float64(maximum)
}

// AlignWords calculates word-level substitutions, deletions, insertions, and
// exact matches using a deterministic minimum-edit alignment.
func AlignWords(original, transcribed []string) WordEdits {
	rows, columns := len(original), len(transcribed)
	matrix := make([][]int, rows+1)
	for row := range matrix {
		matrix[row] = make([]int, columns+1)
		matrix[row][0] = row
	}
	for column := 0; column <= columns; column++ {
		matrix[0][column] = column
	}
	for row := 1; row <= rows; row++ {
		for column := 1; column <= columns; column++ {
			if original[row-1] == transcribed[column-1] {
				matrix[row][column] = matrix[row-1][column-1]
				continue
			}
			matrix[row][column] = 1 + min(
				min(matrix[row-1][column], matrix[row][column-1]),
				matrix[row-1][column-1],
			)
		}
	}

	edits := WordEdits{Distance: matrix[rows][columns]}
	for row, column := rows, columns; row > 0 || column > 0; {
		switch {
		case row > 0 && column > 0 && original[row-1] == transcribed[column-1]:
			edits.Correct++
			row--
			column--
		case row > 0 && column > 0 && matrix[row][column] == matrix[row-1][column-1]+1:
			edits.Substitutions++
			row--
			column--
		case row > 0 && matrix[row][column] == matrix[row-1][column]+1:
			edits.Deletions++
			row--
		default:
			edits.Insertions++
			column--
		}
	}
	return edits
}

// NormalizeSingleLine maps line-breaking whitespace to spaces and collapses
// repeated ASCII spaces. Leading and trailing single spaces are preserved so
// this remains compatible with historical HTR evaluation output.
func NormalizeSingleLine(text string) string {
	text = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text)
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return text
}

// ApplyIgnorePatterns removes unknown markers from the ground truth and skips
// the corresponding rune or word in the transcription.
func ApplyIgnorePatterns(groundTruth, transcription string, patterns []string) (string, string, int) {
	if len(patterns) == 0 {
		return groundTruth, transcription, 0
	}

	patternRunes := make([][]rune, 0, len(patterns))
	ignoredCount := 0
	for _, pattern := range patterns {
		runes := []rune(pattern)
		if len(runes) == 0 {
			continue
		}
		patternRunes = append(patternRunes, runes)
		ignoredCount += strings.Count(groundTruth, pattern) * len(runes)
	}

	groundTruthRunes := []rune(groundTruth)
	transcriptionRunes := []rune(transcription)
	var processedGroundTruth strings.Builder
	var processedTranscription strings.Builder
	groundTruthIndex, transcriptionIndex := 0, 0
	for groundTruthIndex < len(groundTruthRunes) {
		matched := matchingPattern(groundTruthRunes[groundTruthIndex:], patternRunes)
		if len(matched) == 0 {
			processedGroundTruth.WriteRune(groundTruthRunes[groundTruthIndex])
			if transcriptionIndex < len(transcriptionRunes) {
				processedTranscription.WriteRune(transcriptionRunes[transcriptionIndex])
				transcriptionIndex++
			}
			groundTruthIndex++
			continue
		}

		beforeSpace := groundTruthIndex == 0 || unicode.IsSpace(groundTruthRunes[groundTruthIndex-1])
		afterIndex := groundTruthIndex + len(matched)
		afterSpace := afterIndex >= len(groundTruthRunes) || unicode.IsSpace(groundTruthRunes[afterIndex])
		groundTruthIndex = afterIndex
		if beforeSpace && afterSpace {
			for transcriptionIndex < len(transcriptionRunes) && unicode.IsSpace(transcriptionRunes[transcriptionIndex]) {
				transcriptionIndex++
			}
			for transcriptionIndex < len(transcriptionRunes) && !unicode.IsSpace(transcriptionRunes[transcriptionIndex]) {
				transcriptionIndex++
			}
		} else if transcriptionIndex < len(transcriptionRunes) {
			transcriptionIndex++
		}
	}
	return processedGroundTruth.String(), processedTranscription.String(), ignoredCount
}

func matchingPattern(value []rune, patterns [][]rune) []rune {
	for _, pattern := range patterns {
		if len(pattern) > len(value) {
			continue
		}
		matched := true
		for index := range pattern {
			if value[index] != pattern[index] {
				matched = false
				break
			}
		}
		if matched {
			return pattern
		}
	}
	return nil
}

func sequenceDistance[T comparable](left, right []T) int {
	if len(left) < len(right) {
		left, right = right, left
	}
	if len(right) == 0 {
		return len(left)
	}
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for row := 1; row <= len(left); row++ {
		current[0] = row
		for column := 1; column <= len(right); column++ {
			cost := 0
			if left[row-1] != right[column-1] {
				cost = 1
			}
			current[column] = min(
				min(previous[column]+1, current[column-1]+1),
				previous[column-1]+cost,
			)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}
