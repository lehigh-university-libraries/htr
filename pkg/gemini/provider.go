package gemini

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

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Google Gemini vision provider
type Provider struct{}

// New creates a new Gemini provider
func New() *Provider {
	return &Provider{}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "gemini"
}

// ValidateConfig validates the Gemini configuration
func (p *Provider) ValidateConfig(config providers.Config) error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}
	return nil
}

// ExtractText extracts text from an image using Google Gemini Vision API
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", providers.UsageInfo{}, fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}

	// Determine MIME type
	mimeType := mime.TypeByExtension(filepath.Ext(imagePath))
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	// Prepare request body for Gemini API
	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{
						"text": config.Prompt,
					},
					{
						"inline_data": map[string]interface{}{
							"mime_type": mimeType,
							"data":      imageBase64,
						},
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": config.Temperature,
		},
	}

	// Use the specified model, default to gemini-pro-vision if not specified
	model := config.Model
	if model == "gpt-4o" || model == "" {
		model = "gemini-pro-vision"
	}

	requestJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", providers.UsageInfo{}, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: config.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	defer resp.Body.Close()

	// Read response body once for both parsing and error logging
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", providers.UsageInfo{}, fmt.Errorf("gemini API error: %d - %s", resp.StatusCode, string(body))
	}

	var geminiResp map[string]interface{}
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to parse JSON response: %w - body: %s", err, providers.TruncateBody(body))
	}

	// Extract text from Gemini response
	candidates, ok := geminiResp["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return "", providers.UsageInfo{}, fmt.Errorf("no response from Gemini - body: %s", providers.TruncateBody(body))
	}

	candidate, ok := candidates[0].(map[string]interface{})
	if !ok {
		return "", providers.UsageInfo{}, fmt.Errorf("invalid response format from Gemini - body: %s", providers.TruncateBody(body))
	}

	content, ok := candidate["content"].(map[string]interface{})
	if !ok {
		return "", providers.UsageInfo{}, fmt.Errorf("invalid content format from Gemini - body: %s", providers.TruncateBody(body))
	}

	parts, ok := content["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return "", providers.UsageInfo{}, fmt.Errorf("no parts in Gemini response - body: %s", providers.TruncateBody(body))
	}

	part, ok := parts[0].(map[string]interface{})
	if !ok {
		return "", providers.UsageInfo{}, fmt.Errorf("invalid part format from Gemini - body: %s", providers.TruncateBody(body))
	}

	text, ok := part["text"].(string)
	if !ok {
		return "", providers.UsageInfo{}, fmt.Errorf("no text in Gemini response - body: %s", providers.TruncateBody(body))
	}

	// Extract usage metadata if available
	usage := providers.UsageInfo{}
	if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
		if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage.InputTokens = int(promptTokens)
		}
		if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
			usage.OutputTokens = int(candidatesTokens)
		}
	}

	return providers.ProcessResponse(p, text), usage, nil
}
