package utils

import (
	"log/slog"
	"os"
	"regexp"
)

// MaskSensitiveData masks API keys and other sensitive information in strings
// This is used to prevent accidental logging of sensitive data in error messages and URLs
func MaskSensitiveData(s string) string {
	if s == "" {
		return s
	}

	// Mask API keys in URL query parameters (e.g., ?key=xxx or &key=xxx)
	// Matches: key=VALUE, api_key=VALUE, apiKey=VALUE, api-key=VALUE, apikey=VALUE
	keyPattern := regexp.MustCompile(`([?&])(api[_\-]?[kK]ey|key)=([^&\s"]+)`)
	s = keyPattern.ReplaceAllString(s, `${1}${2}=***MASKED***`)

	// Mask Bearer tokens in Authorization headers
	bearerPattern := regexp.MustCompile(`Bearer\s+([A-Za-z0-9_\-\.]+)`)
	s = bearerPattern.ReplaceAllString(s, `Bearer ***MASKED***`)

	// Mask Ocp-Apim-Subscription-Key headers (Azure)
	azureKeyPattern := regexp.MustCompile(`Ocp-Apim-Subscription-Key:\s*([^\s]+)`)
	s = azureKeyPattern.ReplaceAllString(s, `Ocp-Apim-Subscription-Key: ***MASKED***`)

	// Mask x-api-key headers (Anthropic)
	xApiKeyPattern := regexp.MustCompile(`x-api-key:\s*([^\s]+)`)
	s = xApiKeyPattern.ReplaceAllString(s, `x-api-key: ***MASKED***`)

	return s
}

// MaskSensitiveError wraps an error and masks sensitive data when the error is converted to string
func MaskSensitiveError(err error) error {
	if err == nil {
		return nil
	}
	return &maskedError{err: err}
}

type maskedError struct {
	err error
}

func (e *maskedError) Error() string {
	return MaskSensitiveData(e.err.Error())
}

func (e *maskedError) Unwrap() error {
	return e.err
}

func ExitOnError(msg string, err error) {
	slog.Error(msg, "err", MaskSensitiveError(err))
	os.Exit(1)
}
