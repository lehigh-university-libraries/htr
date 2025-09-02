package azure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Azure OCR provider
type Provider struct{}

// New creates a new Azure provider
func New() *Provider {
	return &Provider{}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "azure"
}

// ValidateConfig validates the Azure configuration
func (p *Provider) ValidateConfig(config providers.Config) error {
	endpoint := os.Getenv("AZURE_OCR_ENDPOINT")
	apiKey := os.Getenv("AZURE_OCR_API_KEY")

	if endpoint == "" || apiKey == "" {
		return fmt.Errorf("AZURE_OCR_ENDPOINT and AZURE_OCR_API_KEY environment variables must be set")
	}
	return nil
}

// ExtractText extracts text from an image using Azure Computer Vision Read API
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, error) {
	endpoint := os.Getenv("AZURE_OCR_ENDPOINT")
	apiKey := os.Getenv("AZURE_OCR_API_KEY")

	if endpoint == "" || apiKey == "" {
		return "", fmt.Errorf("AZURE_OCR_ENDPOINT and AZURE_OCR_API_KEY environment variables must be set")
	}

	// Decode base64 image data
	imageData, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 image: %w", err)
	}

	// Azure Computer Vision Read API 3.2 URL (more widely supported)
	readURL := fmt.Sprintf("%s/vision/v3.2/read/analyze", strings.TrimSuffix(endpoint, "/"))

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", readURL, bytes.NewReader(imageData))
	if err != nil {
		return "", err
	}

	// Set headers
	req.Header.Set("Ocp-Apim-Subscription-Key", apiKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("azure OCR API error: %d - %s", resp.StatusCode, string(body))
	}

	// Get the operation URL from the Operation-Location header
	operationURL := resp.Header.Get("Operation-Location")
	if operationURL == "" {
		return "", fmt.Errorf("no operation location returned from Azure OCR")
	}

	// Poll for results
	for attempts := 0; attempts < 30; attempts++ {
		time.Sleep(1 * time.Second)

		req, err := http.NewRequestWithContext(ctx, "GET", operationURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Ocp-Apim-Subscription-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return "", err
		}
		resp.Body.Close()

		status, ok := result["status"].(string)
		if !ok {
			return "", fmt.Errorf("invalid response format from Azure OCR")
		}

		switch status {
		case "succeeded":
			return extractText(result), nil
		case "failed":
			return "", fmt.Errorf("azure OCR analysis failed")
		}
		// Continue polling if status is "running" or "notStarted"
	}

	return "", fmt.Errorf("azure OCR operation timed out")
}

// extractText extracts text from Azure OCR response (supports both v3.2 and v4.0 formats)
func extractText(result map[string]interface{}) string {
	var texts []string

	analyzeResult, ok := result["analyzeResult"].(map[string]interface{})
	if !ok {
		return ""
	}

	// Try v3.2 format first
	readResults, ok := analyzeResult["readResults"].([]interface{})
	if ok {
		for _, readResult := range readResults {
			readResultMap, ok := readResult.(map[string]interface{})
			if !ok {
				continue
			}

			lines, ok := readResultMap["lines"].([]interface{})
			if !ok {
				continue
			}

			for _, line := range lines {
				lineMap, ok := line.(map[string]interface{})
				if !ok {
					continue
				}

				text, ok := lineMap["text"].(string)
				if ok {
					texts = append(texts, text)
				}
			}
		}
	} else {
		// Try v4.0 format as fallback
		pages, ok := analyzeResult["pages"].([]interface{})
		if ok {
			for _, page := range pages {
				pageMap, ok := page.(map[string]interface{})
				if !ok {
					continue
				}

				lines, ok := pageMap["lines"].([]interface{})
				if !ok {
					continue
				}

				for _, line := range lines {
					lineMap, ok := line.(map[string]interface{})
					if !ok {
						continue
					}

					content, ok := lineMap["content"].(string)
					if ok {
						texts = append(texts, content)
					}
				}
			}
		}
	}

	return strings.Join(texts, "\n")
}
