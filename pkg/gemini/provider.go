// Package gemini provides a Google Gemini vision transcription client.
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

const (
	defaultEndpoint         = "https://generativelanguage.googleapis.com/v1beta"
	defaultTimeout          = 2 * time.Minute
	defaultMaxImageBytes    = 50 << 20
	defaultMaxRequestBytes  = 70 << 20
	defaultMaxResponseBytes = 8 << 20
)

// CredentialSource returns an API credential for one request.
type CredentialSource func(context.Context) (string, error)

// Options configures a Client. Constructors do not read environment variables.
type Options struct {
	HTTPClient              *http.Client
	Endpoint                string
	APIKey                  CredentialSource
	Timeout                 time.Duration
	MaxImageBytes           int64
	MaxRequestBytes         int64
	MaxResponseBytes        int64
	MediaResolution         string
	MediaResolutionFallback bool
	IncludeThoughts         bool
}

// Client is a byte-oriented Gemini transcription client.
type Client struct {
	httpClient              *http.Client
	endpoint                string
	apiKey                  CredentialSource
	maxImageBytes           int64
	maxRequestBytes         int64
	maxResponseBytes        int64
	mediaResolution         string
	mediaResolutionFallback bool
	includeThoughts         bool
}

// Provider is the historical CLI adapter. New integrations should use Client.
type Provider struct{}

type generateRequest struct {
	Contents         []content        `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}

type inlineData struct {
	MediaType string `json:"mime_type"`
	Data      string `json:"data"`
}

type generationConfig struct {
	Temperature     float64         `json:"temperature"`
	MediaResolution string          `json:"mediaResolution,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts"`
}

type generateResponse struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Usage struct {
		PromptTokens    int `json:"promptTokenCount"`
		CandidateTokens int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// NewClient constructs a secure Gemini client from explicit dependencies.
func NewClient(options Options) (*Client, error) {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := httpclient.ParseEndpoint(endpoint)
	if err != nil || options.APIKey == nil || !validResolution(options.MediaResolution) {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	return &Client{
		httpClient:              httpclient.Secure(options.HTTPClient, durationOr(options.Timeout, defaultTimeout)),
		endpoint:                parsed.String(),
		apiKey:                  options.APIKey,
		maxImageBytes:           positiveOr(options.MaxImageBytes, defaultMaxImageBytes),
		maxRequestBytes:         positiveOr(options.MaxRequestBytes, defaultMaxRequestBytes),
		maxResponseBytes:        positiveOr(options.MaxResponseBytes, defaultMaxResponseBytes),
		mediaResolution:         options.MediaResolution,
		mediaResolutionFallback: options.MediaResolutionFallback,
		includeThoughts:         options.IncludeThoughts,
	}, nil
}

// Name returns the provider name.
func (c *Client) Name() string { return "gemini" }

// Extract transcribes an encoded image.
func (c *Client) Extract(ctx context.Context, request providers.Request) (providers.Result, error) {
	if err := providers.ValidateRequest(request, c.maxImageBytes); err != nil {
		return providers.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, err)
	}
	mediaType, err := providers.CanonicalMediaType(request.Image.MediaType)
	if err != nil {
		return providers.Result{}, err
	}
	credential, err := c.apiKey(ctx)
	if err != nil {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, err)
	}
	if err := httpclient.SetHeader(make(http.Header), "x-goog-api-key", credential); err != nil {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, err)
	}
	endpoint, err := httpclient.AppendPathSegment(c.endpoint, "/models/", request.Model, ":generateContent")
	if err != nil {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	resolution := c.mediaResolution
	for attempts := 0; attempts < 4; attempts++ {
		result, finishReason, err := c.extractOnce(ctx, endpoint, credential, request, mediaType, resolution)
		if err != nil {
			return providers.Result{}, err
		}
		if finishReason == "MAX_TOKENS" && c.mediaResolutionFallback {
			next := nextResolution(resolution)
			if next != "" {
				resolution = next
				continue
			}
		}
		if strings.TrimSpace(result.Text) == "" {
			return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, http.StatusOK, false, nil)
		}
		return result, nil
	}
	return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
}

func (c *Client) extractOnce(ctx context.Context, endpoint, credential string, request providers.Request, mediaType, resolution string) (providers.Result, string, error) {
	configuration := generationConfig{Temperature: request.Temperature, MediaResolution: resolution}
	if c.includeThoughts {
		configuration.ThinkingConfig = &thinkingConfig{IncludeThoughts: true}
	}
	payload := generateRequest{
		Contents: []content{{Parts: []part{
			{Text: request.Prompt},
			{InlineData: &inlineData{MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(request.Image.Data)}},
		}}},
		GenerationConfig: configuration,
	}
	body, err := json.Marshal(payload)
	if err != nil || int64(len(body)) > c.maxRequestBytes {
		return providers.Result{}, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return providers.Result{}, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if err := httpclient.StaticHeader("x-goog-api-key", credential).Authorize(ctx, httpRequest); err != nil {
		return providers.Result{}, "", providers.ErrorForAuthentication(ctx, err)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return providers.Result{}, "", providers.ErrorForRequest(ctx, err)
	}
	responseBody, readErr := httpclient.ReadAll(response.Body, c.maxResponseBytes)
	_ = response.Body.Close()
	if readErr != nil {
		if errors.Is(readErr, httpclient.ErrResponseTooLarge) {
			return providers.Result{}, "", providers.NewError(providers.ErrorResponseTooLarge, response.StatusCode, false, nil)
		}
		return providers.Result{}, "", providers.ErrorForRequest(ctx, readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return providers.Result{}, "", providers.ErrorForStatus(response.StatusCode)
	}

	var decoded generateResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil || len(decoded.Candidates) == 0 {
		return providers.Result{}, "", providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
	}
	text := ""
	for _, responsePart := range decoded.Candidates[0].Content.Parts {
		if !responsePart.Thought && strings.TrimSpace(responsePart.Text) != "" {
			text = responsePart.Text
			break
		}
	}
	effectiveModel := strings.TrimSpace(decoded.ModelVersion)
	if effectiveModel == "" {
		effectiveModel = request.Model
	}
	return providers.Result{
		Text: providers.CleanResponse(text),
		Usage: providers.UsageInfo{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CandidateTokens,
		},
		EffectiveModel: effectiveModel,
	}, decoded.Candidates[0].FinishReason, nil
}

// New creates the historical CLI adapter.
func New() *Provider { return &Provider{} }

// Name returns the provider name.
func (p *Provider) Name() string { return "gemini" }

// ValidateConfig validates environment-backed CLI configuration.
func (p *Provider) ValidateConfig(providers.Config) error {
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) == "" {
		return providers.NewError(providers.ErrorAuthentication, 0, false, nil)
	}
	return nil
}

// ExtractText adapts historical base64 CLI inputs to Client.
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	if config.Model == "" || config.Model == "gpt-4o" {
		config.Model = "gemini-pro-vision"
	}
	request, err := providers.LegacyRequest(config, imagePath, imageBase64, defaultMaxImageBytes)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	client, err := NewClient(Options{
		APIKey: func(context.Context) (string, error) {
			key := os.Getenv("GEMINI_API_KEY")
			if strings.TrimSpace(key) == "" {
				return "", providers.NewError(providers.ErrorAuthentication, 0, false, nil)
			}
			return key, nil
		},
		Timeout:                 config.Timeout,
		MediaResolution:         config.MaxResolution,
		MediaResolutionFallback: config.MaxResolutionFallback,
		IncludeThoughts:         config.Debug,
	})
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	result, err := client.Extract(ctx, request)
	return result.Text, result.Usage, err
}

func validResolution(value string) bool {
	switch value {
	case "", "MEDIA_RESOLUTION_UNSPECIFIED", "MEDIA_RESOLUTION_HIGH", "MEDIA_RESOLUTION_MEDIUM", "MEDIA_RESOLUTION_LOW":
		return true
	default:
		return false
	}
}

func nextResolution(current string) string {
	switch current {
	case "", "MEDIA_RESOLUTION_UNSPECIFIED":
		return "MEDIA_RESOLUTION_HIGH"
	case "MEDIA_RESOLUTION_HIGH":
		return "MEDIA_RESOLUTION_MEDIUM"
	case "MEDIA_RESOLUTION_MEDIUM":
		return "MEDIA_RESOLUTION_LOW"
	default:
		return ""
	}
}

func positiveOr(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
