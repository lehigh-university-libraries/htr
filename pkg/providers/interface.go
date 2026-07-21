package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultLegacyImageLimit int64 = 50 << 20

const (
	maxModelBytes  = 1 << 10
	maxPromptBytes = 1 << 20
)

var responsePrefixPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(the\s+)?text\s+in\s+(the\s+)?image\s+(is|says|reads):?\s*`),
	regexp.MustCompile(`(?i)^(the\s+)?image\s+contains\s+(the\s+following\s+)?text:?\s*`),
	regexp.MustCompile(`(?i)^here'?s?\s+(the\s+)?text\s+(extracted\s+)?from\s+(the\s+)?image:?\s*`),
	regexp.MustCompile(`(?i)^(i\s+can\s+see\s+)?text\s+(that\s+says|reading):?\s*`),
	regexp.MustCompile(`(?i)^i\s+can\s+see\s+text\s+reading:\s*`),
	regexp.MustCompile(`(?i)^certainly!\s+here'?s?\s+(the\s+)?text\s+(extracted\s+)?from\s+(the\s+)?image:?\s*`),
	regexp.MustCompile(`(?i)^here'?s?\s+the\s+extracted\s+text\s+from\s+(the\s+)?image:?\s*`),
}

// Config represents the configuration for a provider
type Config struct {
	Provider              string
	Model                 string
	Prompt                string
	Temperature           float64
	Timeout               time.Duration
	Debug                 bool
	MaxResolution         string
	MaxResolutionFallback bool
	BaseURL               string
	Audience              string
}

// UsageInfo represents token usage information from a provider
type UsageInfo struct {
	InputTokens  int
	OutputTokens int
}

// Image is an encoded image supplied to a transcription client.
// Data is the original encoded file, not a base64 representation.
type Image struct {
	Data      []byte
	MediaType string
	Filename  string
}

// Request is the provider-neutral input to a transcription client.
type Request struct {
	Model       string
	Prompt      string
	Temperature float64
	Image       Image
}

// Result is the provider-neutral result of a transcription request.
type Result struct {
	Text           string
	Usage          UsageInfo
	EffectiveModel string
}

// Client transcribes encoded image bytes without depending on a filesystem.
type Client interface {
	Name() string
	Extract(context.Context, Request) (Result, error)
}

// ErrorKind categorizes a provider failure without exposing upstream content.
type ErrorKind string

const (
	// ErrorInvalidRequest indicates locally invalid input or a rejected request.
	ErrorInvalidRequest ErrorKind = "invalid_request"
	// ErrorAuthentication indicates missing or rejected credentials.
	ErrorAuthentication ErrorKind = "authentication"
	// ErrorCanceled indicates caller cancellation.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorTimeout indicates a local or upstream timeout.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorTransport indicates a network-level failure.
	ErrorTransport ErrorKind = "transport"
	// ErrorResponseTooLarge indicates that a configured response limit was exceeded.
	ErrorResponseTooLarge ErrorKind = "response_too_large"
	// ErrorRateLimited indicates upstream throttling.
	ErrorRateLimited ErrorKind = "rate_limited"
	// ErrorUpstream indicates an upstream service failure.
	ErrorUpstream ErrorKind = "upstream"
	// ErrorInvalidResponse indicates a malformed or incomplete upstream response.
	ErrorInvalidResponse ErrorKind = "invalid_response"
)

// Error is a deliberately redacted provider error suitable for logs and APIs.
// It never contains request URLs, credentials, response bodies, or model output.
type Error struct {
	Kind       ErrorKind
	StatusCode int
	Retryable  bool
	cause      error
}

// NewError constructs a redacted provider error. Only cancellation and deadline
// causes should normally be retained so errors.Is remains useful without leaking
// transport details such as request URLs.
func NewError(kind ErrorKind, statusCode int, retryable bool, cause error) *Error {
	var safeCause error
	if errors.Is(cause, context.Canceled) {
		safeCause = context.Canceled
	} else if errors.Is(cause, context.DeadlineExceeded) {
		safeCause = context.DeadlineExceeded
	}
	return &Error{Kind: kind, StatusCode: statusCode, Retryable: retryable, cause: safeCause}
}

// Error returns a stable, categorical message.
func (e *Error) Error() string {
	if e == nil {
		return "provider request failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("provider request failed: %s (status %d)", e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("provider request failed: %s", e.Kind)
}

// Unwrap preserves safe context cancellation identity.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// ErrorForStatus maps an HTTP response status to a redacted provider error.
func ErrorForStatus(statusCode int) *Error {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return NewError(ErrorAuthentication, statusCode, false, nil)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return NewError(ErrorTimeout, statusCode, true, nil)
	case http.StatusTooManyRequests:
		return NewError(ErrorRateLimited, statusCode, true, nil)
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed,
		http.StatusConflict, http.StatusUnprocessableEntity,
		http.StatusRequestEntityTooLarge:
		return NewError(ErrorInvalidRequest, statusCode, false, nil)
	default:
		return NewError(ErrorUpstream, statusCode, statusCode >= 500, nil)
	}
}

// ErrorForRequest maps a request failure while retaining only safe context identity.
func ErrorForRequest(ctx context.Context, err error) *Error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return NewError(ErrorCanceled, 0, false, context.Canceled)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return NewError(ErrorTimeout, 0, true, context.DeadlineExceeded)
	}
	return NewError(ErrorTransport, 0, true, nil)
}

// ErrorForAuthentication maps an authentication failure while preserving only
// safe context cancellation identity.
func ErrorForAuthentication(ctx context.Context, err error) *Error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorForRequest(ctx, err)
	}
	return NewError(ErrorAuthentication, 0, false, nil)
}

// ValidateRequest checks provider-neutral request invariants before any network call.
func ValidateRequest(request Request, maxImageBytes int64) error {
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Model) != request.Model ||
		len(request.Model) > maxModelBytes || strings.ContainsAny(request.Model, "\r\n\x00") ||
		strings.TrimSpace(request.Prompt) == "" || len(request.Prompt) > maxPromptBytes || strings.ContainsRune(request.Prompt, '\x00') {
		return NewError(ErrorInvalidRequest, 0, false, nil)
	}
	if request.Temperature < 0 || math.IsNaN(request.Temperature) || math.IsInf(request.Temperature, 0) {
		return NewError(ErrorInvalidRequest, 0, false, nil)
	}
	return ValidateImage(request.Image, maxImageBytes)
}

// ValidateImage checks byte-oriented image invariants before any network call.
func ValidateImage(image Image, maxImageBytes int64) error {
	if len(image.Data) == 0 || maxImageBytes <= 0 || int64(len(image.Data)) > maxImageBytes {
		return NewError(ErrorInvalidRequest, 0, false, nil)
	}
	if _, err := CanonicalMediaType(image.MediaType); err != nil {
		return NewError(ErrorInvalidRequest, 0, false, nil)
	}
	return nil
}

// CanonicalMediaType returns a normalized image media type without parameters.
func CanonicalMediaType(raw string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(raw)
	mediaType = strings.ToLower(mediaType)
	if err != nil || !strings.HasPrefix(mediaType, "image/") {
		return "", NewError(ErrorInvalidRequest, 0, false, nil)
	}
	return mediaType, nil
}

// LegacyRequest converts the historical base64/file-name inputs into a bounded
// byte-oriented request. It exists only for CLI compatibility.
func LegacyRequest(config Config, imagePath, imageBase64 string, maxImageBytes int64) (Request, error) {
	if maxImageBytes <= 0 {
		maxImageBytes = defaultLegacyImageLimit
	}
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(imageBase64))
	data, err := io.ReadAll(io.LimitReader(decoder, maxImageBytes+1))
	if err != nil {
		return Request{}, NewError(ErrorInvalidRequest, 0, false, nil)
	}
	if int64(len(data)) > maxImageBytes {
		return Request{}, NewError(ErrorInvalidRequest, 0, false, nil)
	}

	mediaType := mime.TypeByExtension(filepath.Ext(imagePath))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	if parsed, _, parseErr := mime.ParseMediaType(mediaType); parseErr == nil {
		mediaType = parsed
	}

	request := Request{
		Model:       config.Model,
		Prompt:      config.Prompt,
		Temperature: config.Temperature,
		Image: Image{
			Data:      data,
			MediaType: mediaType,
			Filename:  filepath.Base(imagePath),
		},
	}
	if err := ValidateRequest(request, maxImageBytes); err != nil {
		return Request{}, err
	}
	return request, nil
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

	for _, pattern := range responsePrefixPatterns {
		response = pattern.ReplaceAllString(response, "")
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
