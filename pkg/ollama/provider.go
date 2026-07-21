// Package ollama provides an Ollama vision transcription client.
package ollama

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

	"github.com/lehigh-university-libraries/htr/pkg/auth/gcpidtoken"
	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

const (
	defaultEndpoint         = "http://localhost:11434"
	defaultTimeout          = 2 * time.Minute
	defaultMaxImageBytes    = 50 << 20
	defaultMaxRequestBytes  = 70 << 20
	defaultMaxResponseBytes = 8 << 20
)

// Options configures a Client. Constructors do not read environment variables.
type Options struct {
	HTTPClient       *http.Client
	Endpoint         string
	Authenticator    httpclient.Authenticator
	Timeout          time.Duration
	MaxImageBytes    int64
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

// Client is a byte-oriented Ollama transcription client.
type Client struct {
	httpClient       *http.Client
	endpoint         string
	authenticator    httpclient.Authenticator
	maxImageBytes    int64
	maxRequestBytes  int64
	maxResponseBytes int64
}

// Provider is the historical CLI adapter. New integrations should use Client.
type Provider struct {
	identityTokens *gcpidtoken.Source
}

type generateRequest struct {
	Model   string   `json:"model"`
	Prompt  string   `json:"prompt"`
	Images  []string `json:"images"`
	Stream  bool     `json:"stream"`
	Options struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
}

type generateResponse struct {
	Model           string `json:"model"`
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// NewClient constructs a secure Ollama client from explicit dependencies.
func NewClient(options Options) (*Client, error) {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	generateEndpoint, err := httpclient.AppendPath(endpoint, "/api/generate")
	if err != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	authenticator := options.Authenticator
	if authenticator == nil {
		authenticator = httpclient.NoAuth{}
	}
	return &Client{
		httpClient:       httpclient.Secure(options.HTTPClient, durationOr(options.Timeout, defaultTimeout)),
		endpoint:         generateEndpoint,
		authenticator:    authenticator,
		maxImageBytes:    positiveOr(options.MaxImageBytes, defaultMaxImageBytes),
		maxRequestBytes:  positiveOr(options.MaxRequestBytes, defaultMaxRequestBytes),
		maxResponseBytes: positiveOr(options.MaxResponseBytes, defaultMaxResponseBytes),
	}, nil
}

// Name returns the provider name.
func (c *Client) Name() string { return "ollama" }

// Extract transcribes an encoded image.
func (c *Client) Extract(ctx context.Context, request providers.Request) (providers.Result, error) {
	if err := providers.ValidateRequest(request, c.maxImageBytes); err != nil {
		return providers.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, err)
	}
	payload := generateRequest{
		Model:  request.Model,
		Prompt: request.Prompt,
		Images: []string{base64.StdEncoding.EncodeToString(request.Image.Data)},
		Stream: false,
	}
	payload.Options.Temperature = request.Temperature
	body, err := json.Marshal(payload)
	if err != nil || int64(len(body)) > c.maxRequestBytes {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if err := c.authenticator.Authorize(ctx, httpRequest); err != nil {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, err)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, err)
	}
	defer response.Body.Close()
	responseBody, err := httpclient.ReadAll(response.Body, c.maxResponseBytes)
	if err != nil {
		if errors.Is(err, httpclient.ErrResponseTooLarge) {
			return providers.Result{}, providers.NewError(providers.ErrorResponseTooLarge, response.StatusCode, false, nil)
		}
		return providers.Result{}, providers.ErrorForRequest(ctx, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return providers.Result{}, providers.ErrorForStatus(response.StatusCode)
	}
	var decoded generateResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil || strings.TrimSpace(decoded.Response) == "" {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
	}
	effectiveModel := strings.TrimSpace(decoded.Model)
	if effectiveModel == "" {
		effectiveModel = request.Model
	}
	return providers.Result{
		Text: providers.CleanResponse(decoded.Response),
		Usage: providers.UsageInfo{
			InputTokens:  decoded.PromptEvalCount,
			OutputTokens: decoded.EvalCount,
		},
		EffectiveModel: effectiveModel,
	}, nil
}

// New creates the historical CLI adapter.
func New() *Provider {
	source, err := gcpidtoken.New(gcpidtoken.Options{})
	if err != nil {
		return &Provider{}
	}
	return &Provider{identityTokens: source}
}

// Name returns the provider name.
func (p *Provider) Name() string { return "ollama" }

// ValidateConfig validates the configured CLI endpoint without contacting it.
func (p *Provider) ValidateConfig(config providers.Config) error {
	_, err := httpclient.ParseEndpoint(resolveBaseURL(config))
	if err != nil {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	return nil
}

// ExtractText adapts historical base64 CLI inputs to Client.
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	if config.Model == "" || config.Model == "gpt-4o" {
		config.Model = "llava"
	}
	request, err := providers.LegacyRequest(config, imagePath, imageBase64, defaultMaxImageBytes)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	baseURL := resolveBaseURL(config)
	var authenticator httpclient.Authenticator = httpclient.NoAuth{}
	if audience := resolveAudience(config, baseURL); audience != "" {
		tokenSource := p.identityTokens
		if tokenSource == nil {
			tokenSource, err = gcpidtoken.New(gcpidtoken.Options{})
			if err != nil {
				return "", providers.UsageInfo{}, providers.NewError(providers.ErrorAuthentication, 0, false, nil)
			}
		}
		authenticator = httpclient.BearerAuthenticator{Source: tokenSource, Audience: audience}
	}
	client, err := NewClient(Options{Endpoint: baseURL, Authenticator: authenticator, Timeout: config.Timeout})
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	result, err := client.Extract(ctx, request)
	return result.Text, result.Usage, err
}

func resolveBaseURL(config providers.Config) string {
	if baseURL := strings.TrimSpace(config.BaseURL); baseURL != "" {
		return baseURL
	}
	if environmentURL := strings.TrimSpace(os.Getenv("OLLAMA_URL")); environmentURL != "" {
		return environmentURL
	}
	return defaultEndpoint
}

func resolveAudience(config providers.Config, baseURL string) string {
	if audience := strings.TrimSpace(config.Audience); audience != "" {
		return audience
	}
	if audience := strings.TrimSpace(os.Getenv("OLLAMA_AUDIENCE")); audience != "" {
		return audience
	}
	parsed, err := httpclient.ParseEndpoint(baseURL)
	if err != nil || parsed.Scheme != "https" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".run.app") {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
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
