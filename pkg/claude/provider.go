package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Anthropic Claude vision provider
type Provider struct {
	client   *http.Client
	endpoint string
}

// DefaultModel is the current balanced Claude vision model.
const DefaultModel = "claude-sonnet-5-5"

// Response represents an Anthropic API response
type Response struct {
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// New creates a new Claude provider
func New() *Provider {
	return &Provider{}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "claude"
}

// ValidateConfig validates the Claude configuration
func (p *Provider) ValidateConfig(config providers.Config) error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}
	return nil
}

// ExtractText extracts text from an image using Claude's vision API
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", providers.UsageInfo{}, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	if config.Model == "" {
		config.Model = DefaultModel
	}
	imageRequest, err := providers.LegacyRequest(config, imagePath, imageBase64, 50<<20)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	mediaType := imageRequest.Image.MediaType
	limit := config.MaxOutputTokens
	if limit == 0 {
		limit = 4096
		if modernModel(config.Model) {
			limit = 16000
		}
	}
	if limit < 0 {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}

	// Prepare request body for Claude API
	requestBody := map[string]interface{}{
		"model":      config.Model,
		"max_tokens": limit,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": mediaType,
							"data":       imageBase64,
						},
					},
					{
						"type": "text",
						"text": config.Prompt,
					},
				},
			},
		},
	}

	// Add temperature if specified
	if !modernModel(config.Model) && config.Temperature > 0 {
		requestBody["temperature"] = config.Temperature
	}

	if config.ReasoningEffort != "" {
		switch config.ReasoningEffort {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
		}
		requestBody["output_config"] = map[string]string{"effort": config.ReasoningEffort}
	}
	requestJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	endpoint := p.endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", providers.UsageInfo{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	client := httpclient.Secure(p.client, timeout)
	resp, err := client.Do(req)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	defer resp.Body.Close()

	// Read response body once for both parsing and error logging
	body, err := httpclient.ReadAll(resp.Body, 8<<20)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", providers.UsageInfo{}, providers.ErrorForStatus(resp.StatusCode)
	}

	var claudeResp Response
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidResponse, resp.StatusCode, false, nil)
	}

	if len(claudeResp.Content) == 0 {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidResponse, resp.StatusCode, false, nil)
	}

	// Join all text blocks, excluding adaptive thinking.
	var extractedText strings.Builder
	for _, content := range claudeResp.Content {
		if content.Type == "text" {
			extractedText.WriteString(content.Text)
		}
	}

	usage := providers.UsageInfo{InputTokens: claudeResp.Usage.InputTokens, OutputTokens: claudeResp.Usage.OutputTokens}
	if claudeResp.StopReason == "max_tokens" {
		return extractedText.String(), usage, providers.NewError(providers.ErrorOutputLimit, resp.StatusCode, false, nil)
	}
	if extractedText.Len() == 0 {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidResponse, resp.StatusCode, false, nil)
	}

	return providers.ProcessResponse(p, extractedText.String()), usage, nil
}

func modernModel(model string) bool {
	for _, prefix := range []string{"claude-sonnet-5", "claude-opus-5", "claude-haiku-5", "claude-fable-5", "claude-opus-4-7"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}
