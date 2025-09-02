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
	"regexp"
	"strings"
	"time"

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
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY environment variable not set")
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
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini API error: %d - %s", resp.StatusCode, string(body))
	}

	var geminiResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", err
	}

	// Extract text from Gemini response
	candidates, ok := geminiResp["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return "", fmt.Errorf("no response from Gemini")
	}

	candidate, ok := candidates[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid response format from Gemini")
	}

	content, ok := candidate["content"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid content format from Gemini")
	}

	parts, ok := content["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return "", fmt.Errorf("no parts in Gemini response")
	}

	part, ok := parts[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid part format from Gemini")
	}

	text, ok := part["text"].(string)
	if !ok {
		return "", fmt.Errorf("no text in Gemini response")
	}

	return cleanResponse(text), nil
}

// cleanResponse cleans up Gemini API responses
func cleanResponse(response string) string {
	response = strings.TrimSpace(response)

	// Remove common prefixes from Gemini responses
	prefixPatterns := []string{
		`(?i)^(the\s+)?text\s+in\s+(the\s+)?image\s+(is|says|reads):?\s*`,
		`(?i)^(the\s+)?image\s+contains\s+(the\s+following\s+)?text:?\s*`,
		`(?i)^here'?s?\s+(the\s+)?text\s+from\s+(the\s+)?image:?\s*`,
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
