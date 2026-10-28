package metrics

import (
	"html"
	"regexp"
	"strings"
)

var markdownPatterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?m)^\s{0,3}(?:\x60{3,}|~{3,})[^\n]*$`), ""},
	{regexp.MustCompile(`!\[[^\]\n]*\]\([^\n)]*\)`), ""},
	{regexp.MustCompile(`\[([^\]\n]+)\]\([^\n)]*\)`), "$1"},
	{regexp.MustCompile(`(?m)^\s*(?:={3,}|-{3,})\s*$`), ""},
	{regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+|\s+#+\s*$`), ""},
	{regexp.MustCompile(`(?m)^\s*(?:>\s*)+`), ""},
	{regexp.MustCompile(`(?m)^\s*[-+*]\s+`), ""},
	{regexp.MustCompile(`<[^>]*>`), " "},
	{regexp.MustCompile("`+([^`]+)`+"), "$1"},
	{regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`), "$1$2"},
	{regexp.MustCompile(`\*([^*\n]+)\*`), "$1"},
	{regexp.MustCompile(`\b_([^_\n]+)_\b`), "$1"},
	{regexp.MustCompile(`\${1,2}([^\s$](?:[^$\n]*[^\s$])?)\${1,2}`), "$1"},
}

// NormalizeMarkdown removes presentation markup while preserving textual content.
// It is intentionally a text normalization, not an HTML renderer or sanitizer.
func NormalizeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, p := range markdownPatterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
