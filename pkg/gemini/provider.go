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

	// Helper to determine next resolution
	getNextResolution := func(current string) string {
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

	currentResolution := config.MaxResolution
	
	// Use the specified model, default to gemini-pro-vision if not specified
	model := config.Model
	if model == "gpt-4o" || model == "" {
		model = "gemini-pro-vision"
	}
	
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	for {
		generationConfig := map[string]any{
			"temperature": config.Temperature,
		}
		if currentResolution != "" && currentResolution != "MEDIA_RESOLUTION_UNSPECIFIED" {
			generationConfig["mediaResolution"] = currentResolution
		}

		// Prepare request body for Gemini API
		requestBody := map[string]any{
			"contents": []map[string]any{
				{
					"parts": []map[string]any{
						{
							"text": config.Prompt,
						},
						{
							"inline_data": map[string]any{
								"mime_type": mimeType,
								"data":      imageBase64,
							},
						},
					},
				},
			},
			"generationConfig": generationConfig,
		}

		requestJSON, err := json.Marshal(requestBody)
		if err != nil {
			return "", providers.UsageInfo{}, fmt.Errorf("failed to marshal request: %w", err)
		}

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

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", providers.UsageInfo{}, fmt.Errorf("failed to read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", providers.UsageInfo{}, fmt.Errorf("gemini API error: %d - %s", resp.StatusCode, string(body))
		}

		var geminiResp map[string]any
		if err := json.Unmarshal(body, &geminiResp); err != nil {
			return "", providers.UsageInfo{}, fmt.Errorf("failed to parse JSON response: %w - body: %s", err, providers.TruncateBody(body))
		}

		// Extract text from Gemini response
		candidates, ok := geminiResp["candidates"].([]any)
		if !ok || len(candidates) == 0 {
			// Check prompt feedback if no candidates
			if promptFeedback, ok := geminiResp["promptFeedback"].(map[string]any); ok {
				if blockReason, ok := promptFeedback["blockReason"]; ok {
					return "", providers.UsageInfo{}, fmt.Errorf("blocked: %v", blockReason)
				}
			}
			return "", providers.UsageInfo{}, fmt.Errorf("no response from Gemini - body: %s", providers.TruncateBody(body))
		}

		candidate, ok := candidates[0].(map[string]any)
		if !ok {
			return "", providers.UsageInfo{}, fmt.Errorf("invalid response format from Gemini - body: %s", providers.TruncateBody(body))
		}

		// Check finish reason for fallback
		finishReason, _ := candidate["finishReason"].(string)
		if finishReason == "MAX_TOKENS" && config.MaxResolutionFallback {
			nextRes := getNextResolution(currentResolution)
			if nextRes != "" {
				slog.Error("Hit MAX_TOKENS limit with resolution. Retrying", "currentResolution", currentResolution, "nextRes", nextRes)
				currentResolution = nextRes
				continue
			}
		}

		content, ok := candidate["content"].(map[string]any)
		if !ok {
			return "", providers.UsageInfo{}, fmt.Errorf("invalid content format from Gemini - body: %s", providers.TruncateBody(body))
		}

		parts, ok := content["parts"].([]any)
		if !ok || len(parts) == 0 {
			return "", providers.UsageInfo{}, fmt.Errorf("no parts in Gemini response - body: %s", providers.TruncateBody(body))
		}

		part, ok := parts[0].(map[string]any)
		if !ok {
			return "", providers.UsageInfo{}, fmt.Errorf("invalid part format from Gemini - body: %s", providers.TruncateBody(body))
		}

		text, ok := part["text"].(string)
		if !ok {
			return "", providers.UsageInfo{}, fmt.Errorf("no text in Gemini response - body: %s", providers.TruncateBody(body))
		}

		// Extract usage metadata if available
		usage := providers.UsageInfo{}
		if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]any); ok {
			if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
				usage.InputTokens = int(promptTokens)
			}
			if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
				usage.OutputTokens = int(candidatesTokens)
			}
		}

		return providers.ProcessResponse(p, text), usage, nil
	}
}
