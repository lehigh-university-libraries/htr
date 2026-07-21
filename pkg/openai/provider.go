// Package openai provides an OpenAI vision transcription client.
package openai

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
	defaultEndpoint         = "https://api.openai.com/v1/chat/completions"
	defaultTimeout          = 2 * time.Minute
	defaultMaxImageBytes    = 50 << 20
	defaultMaxRequestBytes  = 70 << 20
	defaultMaxResponseBytes = 8 << 20
)

// CredentialSource returns an API credential for one request.
type CredentialSource func(context.Context) (string, error)

// Options configures a Client. Constructors do not read environment variables.
type Options struct {
	HTTPClient       *http.Client
	Endpoint         string
	APIKey           CredentialSource
	Timeout          time.Duration
	MaxImageBytes    int64
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

// Client is a byte-oriented OpenAI transcription client.
type Client struct {
	httpClient       *http.Client
	endpoint         string
	apiKey           CredentialSource
	maxImageBytes    int64
	maxRequestBytes  int64
	maxResponseBytes int64
}

// Provider is the historical CLI adapter. New integrations should use Client.
type Provider struct{}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// NewClient constructs a secure OpenAI client from explicit dependencies.
func NewClient(options Options) (*Client, error) {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := httpclient.ParseEndpoint(endpoint)
	if err != nil || options.APIKey == nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	maxImageBytes := positiveOr(options.MaxImageBytes, defaultMaxImageBytes)
	maxRequestBytes := positiveOr(options.MaxRequestBytes, defaultMaxRequestBytes)
	maxResponseBytes := positiveOr(options.MaxResponseBytes, defaultMaxResponseBytes)
	return &Client{
		httpClient:       httpclient.Secure(options.HTTPClient, durationOr(options.Timeout, defaultTimeout)),
		endpoint:         parsed.String(),
		apiKey:           options.APIKey,
		maxImageBytes:    maxImageBytes,
		maxRequestBytes:  maxRequestBytes,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

// Name returns the provider name.
func (c *Client) Name() string { return "openai" }

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
	if err := httpclient.SetHeader(make(http.Header), "Authorization", "Bearer "+credential); err != nil {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, err)
	}
	payload := chatRequest{
		Model:       request.Model,
		Temperature: request.Temperature,
		Messages: []chatMessage{{
			Role: "user",
			Content: []contentPart{
				{Type: "text", Text: request.Prompt},
				{Type: "image_url", ImageURL: &imageURL{URL: "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(request.Image.Data)}},
			},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil || int64(len(body)) > c.maxRequestBytes {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if err := httpclient.StaticBearer(credential).Authorize(ctx, httpRequest); err != nil {
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

	var decoded chatResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil || len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
	}
	effectiveModel := strings.TrimSpace(decoded.Model)
	if effectiveModel == "" {
		effectiveModel = request.Model
	}
	return providers.Result{
		Text: providers.CleanResponse(decoded.Choices[0].Message.Content),
		Usage: providers.UsageInfo{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
		},
		EffectiveModel: effectiveModel,
	}, nil
}

// New creates the historical CLI adapter.
func New() *Provider { return &Provider{} }

// Name returns the provider name.
func (p *Provider) Name() string { return "openai" }

// ValidateConfig validates environment-backed CLI configuration.
func (p *Provider) ValidateConfig(providers.Config) error {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return providers.NewError(providers.ErrorAuthentication, 0, false, nil)
	}
	return nil
}

// ExtractText adapts historical base64 CLI inputs to Client.
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	request, err := providers.LegacyRequest(config, imagePath, imageBase64, defaultMaxImageBytes)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	client, err := NewClient(Options{
		APIKey: func(context.Context) (string, error) {
			key := os.Getenv("OPENAI_API_KEY")
			if strings.TrimSpace(key) == "" {
				return "", providers.NewError(providers.ErrorAuthentication, 0, false, nil)
			}
			return key, nil
		},
		Timeout: config.Timeout,
	})
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	result, err := client.Extract(ctx, request)
	return result.Text, result.Usage, err
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
