package providers

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Config represents the configuration for a provider
type Config struct {
	Provider      string
	Model         string
	Prompt        string
	Temperature   float64
	Timeout       time.Duration
	MaxResolution         string
	MaxResolutionFallback bool
}

// UsageInfo represents token usage information from a provider
type UsageInfo struct {
	InputTokens  int
	OutputTokens int
}

// Provider interface that all OCR/vision providers must implement
type Provider interface {
	// ExtractText extracts text from an image using the provider's API
	// Returns the extracted text and usage information (tokens used)
	ExtractText(ctx context.Context, config Config, imagePath, imageBase64 string) (string, UsageInfo, error)
	// Name returns the provider's name
	Name() string
	// ValidateConfig validates the provider-specific configuration
	ValidateConfig(config Config) error
}

// CleanResponseProvider is an optional interface that providers can implement
// to provide custom response cleaning logic
type CleanResponseProvider interface {
	CleanResponse(response string) string
}

// CleanResponse provides general response cleaning that works for most AI providers
func CleanResponse(response string) string {
	response = strings.TrimSpace(response)

	// Remove common prefixes from AI responses (case insensitive)
	prefixPatterns := []string{
		`(?i)^(the\s+)?text\s+in\s+(the\s+)?image\s+(is|says|reads):?\s*`,
		`(?i)^(the\s+)?image\s+contains\s+(the\s+following\s+)?text:?\s*`,
		`(?i)^here'?s?\s+(the\s+)?text\s+(extracted\s+)?from\s+(the\s+)?image:?\s*`,
		`(?i)^(i\s+can\s+see\s+)?text\s+(that\s+says|reading):?\s*`,
		`(?i)^i\s+can\s+see\s+text\s+reading:\s*`,
		`(?i)^certainly!\s+here'?s?\s+(the\s+)?text\s+(extracted\s+)?from\s+(the\s+)?image:?\s*`,
		`(?i)^here'?s?\s+the\s+extracted\s+text\s+from\s+(the\s+)?image:?\s*`,
	}

	for _, pattern := range prefixPatterns {
		re := regexp.MustCompile(pattern)
		response = re.ReplaceAllString(response, "")
		response = strings.TrimSpace(response)
	}

	// Remove surrounding quotes
	response = strings.Trim(response, `"'`)

	// Remove markdown code blocks if present
	if strings.HasPrefix(response, "```") && strings.HasSuffix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	return response
}

// ProcessResponse cleans a response using the provider's custom cleaner if available,
// otherwise uses the general CleanResponse function
func ProcessResponse(provider Provider, response string) string {
	if cleaner, ok := provider.(CleanResponseProvider); ok {
		return cleaner.CleanResponse(response)
	}
	return CleanResponse(response)
}

// TruncateBody truncates a response body to a maximum length for error messages.
// This helps keep error logs readable while still providing context.
// Default maxLen is 500 if not specified.
func TruncateBody(body []byte, maxLen ...int) string {
	limit := 500
	if len(maxLen) > 0 && maxLen[0] > 0 {
		limit = maxLen[0]
	}
	s := string(body)
	if len(s) > limit {
		return s[:limit] + "... (truncated)"
	}
	return s
}
