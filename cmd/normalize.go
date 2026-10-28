package cmd

import "fmt"

func validateNormalization(s string) error {
	if s != "" && s != "none" && s != "markdown" {
		return fmt.Errorf("invalid normalization %q: use none or markdown", s)
	}
	return nil
}
func copyAccuracyMetrics(dst *EvalResult, m EvalResult) {
	dst.CharacterSimilarity = m.CharacterSimilarity
	dst.CharacterAccuracy = m.CharacterAccuracy
	dst.WordSimilarity = m.WordSimilarity
	dst.WordAccuracy = m.WordAccuracy
	dst.WordErrorRate = m.WordErrorRate
	dst.TotalWordsOriginal = m.TotalWordsOriginal
	dst.TotalWordsTranscribed = m.TotalWordsTranscribed
	dst.CorrectWords = m.CorrectWords
	dst.Substitutions = m.Substitutions
	dst.Deletions = m.Deletions
	dst.Insertions = m.Insertions
	dst.IgnoredCharsCount = m.IgnoredCharsCount
}
