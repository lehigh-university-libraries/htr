package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Anthropic Claude vision provider
type Provider struct{}

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

	// Determine media type (Claude uses "media_type" instead of "mime_type")
	mediaType := mime.TypeByExtension(filepath.Ext(imagePath))
	if mediaType == "" {
		mediaType = "image/jpeg"
	}

	// Prepare request body for Claude API
	requestBody := map[string]interface{}{
		"model":      config.Model,
		"max_tokens": 4096,
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
	if config.Temperature > 0 {
		requestBody["temperature"] = config.Temperature
	}

	requestJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", providers.UsageInfo{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", providers.UsageInfo{}, fmt.Errorf("claude API error: %d - %s", resp.StatusCode, string(body))
	}

	var claudeResp Response
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return "", providers.UsageInfo{}, err
	}

	if len(claudeResp.Content) == 0 {
		return "", providers.UsageInfo{}, fmt.Errorf("no response from Claude")
	}

	// Extract text from the first text content block
	var extractedText string
	for _, content := range claudeResp.Content {
		if content.Type == "text" {
			extractedText = content.Text
			break
		}
	}

	if extractedText == "" {
		return "", providers.UsageInfo{}, fmt.Errorf("no text content in Claude response")
	}

	usage := providers.UsageInfo{
		InputTokens:  claudeResp.Usage.InputTokens,
		OutputTokens: claudeResp.Usage.OutputTokens,
	}

	return providers.ProcessResponse(p, extractedText), usage, nil
}
