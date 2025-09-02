package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Ollama local provider
type Provider struct{}

// New creates a new Ollama provider
func New() *Provider {
	return &Provider{}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "ollama"
}

// ValidateConfig validates the Ollama configuration
func (p *Provider) ValidateConfig(config providers.Config) error {
	// We could ping the API here, but for now just validate the URL format
	return nil
}

// ExtractText extracts text from an image using Ollama local API
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, error) {
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434" // Default Ollama URL
	}

	// Use the specified model, default to llava if not specified
	model := config.Model
	if model == "gpt-4o" || model == "" {
		model = "llava"
	}

	// Prepare request body for Ollama API
	requestBody := map[string]interface{}{
		"model":  model,
		"prompt": config.Prompt,
		"images": []string{imageBase64},
		"stream": false,
		"options": map[string]interface{}{
			"temperature": config.Temperature,
		},
	}

	requestJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	url := fmt.Sprintf("%s/api/generate", strings.TrimSuffix(ollamaURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 300 * time.Second} // Longer timeout for local inference
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API error: %d - %s", resp.StatusCode, string(body))
	}

	var ollamaResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", err
	}

	// Extract response from Ollama
	response, ok := ollamaResp["response"].(string)
	if !ok {
		return "", fmt.Errorf("no response from Ollama")
	}

	return providers.ProcessResponse(p, response), nil
}
