// Package azure provides Azure Document Intelligence and legacy Read OCR.
package azure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// DefaultModel is Microsoft's document-focused OCR model (GA API 2024-11-30).
const DefaultModel = "prebuilt-read"

// Provider implements Azure OCR. Use read-v3.2 to retain the legacy Vision API.
type Provider struct {
	client       *http.Client
	pollInterval time.Duration
}

// New creates an Azure provider.
func New() *Provider { return &Provider{} }

// Name returns the provider name.
func (p *Provider) Name() string { return "azure" }

// ValidateConfig validates environment-backed endpoint and key configuration.
func (p *Provider) ValidateConfig(config providers.Config) error {
	if strings.TrimSpace(os.Getenv("AZURE_OCR_ENDPOINT")) == "" || strings.TrimSpace(os.Getenv("AZURE_OCR_API_KEY")) == "" {
		return fmt.Errorf("AZURE_OCR_ENDPOINT and AZURE_OCR_API_KEY environment variables must be set")
	}
	if _, err := httpclient.ParseEndpoint(os.Getenv("AZURE_OCR_ENDPOINT")); err != nil {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	return nil
}

// ExtractText submits an image and polls the same origin under one deadline.
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	if err := p.ValidateConfig(config); err != nil {
		return "", providers.UsageInfo{}, err
	}
	if config.Model == "" {
		config.Model = DefaultModel
	}
	if base64.StdEncoding.DecodedLen(len(imageBase64)) > 50<<20 {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	imageData, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil || len(imageData) == 0 || len(imageData) > 50<<20 {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	raw, err := analyzeImage(ctx, os.Getenv("AZURE_OCR_ENDPOINT"), os.Getenv("AZURE_OCR_API_KEY"), config.Model, imageData, config.Timeout, p.client, p.pollInterval)
	if err != nil {
		return "", providers.UsageInfo{}, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
	}
	return extractText(result), providers.UsageInfo{}, nil
}

func analyzeImage(ctx context.Context, endpoint, key, model string, imageData []byte, timeout time.Duration, injected *http.Client, pollInterval time.Duration) ([]byte, error) {
	// Older eval files used gpt-4o even though Azure ignored that model field.
	legacy := model == "read-v3.2" || model == "gpt-4o"

	readURL, err := httpclient.AppendPathSegment(endpoint, "/documentintelligence/documentModels/", model, ":analyze")
	if legacy {
		readURL, err = httpclient.AppendPath(endpoint, "/vision/v3.2/read/analyze")
	}
	if err != nil {
		return nil, err
	}
	if !legacy {
		readURL += "?api-version=2024-11-30"
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := httpclient.Secure(injected, timeout)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, readURL, bytes.NewReader(imageData))
	if err != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	auth := httpclient.StaticHeader("Ocp-Apim-Subscription-Key", key)
	if err := auth.Authorize(ctx, request); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, providers.ErrorForRequest(ctx, err)
	}
	_, readErr := httpclient.ReadAll(response.Body, 16<<20)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, providers.ErrorForRequest(ctx, closeErr)
	}
	if response.StatusCode != http.StatusAccepted {
		return nil, providers.ErrorForStatus(response.StatusCode)
	}
	operation, err := url.Parse(response.Header.Get("Operation-Location"))
	// Polling URLs are untrusted: never send the resource key to another origin.
	if err != nil || operation.User != nil || operation.Fragment != "" || operation.Scheme != request.URL.Scheme || !strings.EqualFold(operation.Host, request.URL.Host) || operation.Path == "" {
		return nil, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
	}
	interval := pollInterval
	if interval <= 0 {
		interval = time.Second
	}
	for {
		poll, err := http.NewRequestWithContext(ctx, http.MethodGet, operation.String(), nil)
		if err != nil {
			return nil, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
		}
		if err := auth.Authorize(ctx, poll); err != nil {
			return nil, err
		}
		response, err := client.Do(poll)
		if err != nil {
			return nil, providers.ErrorForRequest(ctx, err)
		}
		body, readErr := httpclient.ReadAll(response.Body, 16<<20)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, providers.ErrorForRequest(ctx, closeErr)
		}
		if response.StatusCode != http.StatusOK {
			return nil, providers.ErrorForStatus(response.StatusCode)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
		}
		switch result["status"] {
		case "succeeded":
			return body, nil
		case "failed", "canceled":
			return nil, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
		case "running", "notStarted":
		default:
			return nil, providers.NewError(providers.ErrorInvalidResponse, response.StatusCode, false, nil)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, providers.ErrorForRequest(ctx, ctx.Err())
		case <-timer.C:
		}
	}
}

// extractText extracts text from Azure OCR response (supports both v3.2 and v4.0 formats)
func extractText(result map[string]interface{}) string {
	var texts []string

	analyzeResult, ok := result["analyzeResult"].(map[string]interface{})
	if !ok {
		return ""
	}

	if content, ok := analyzeResult["content"].(string); ok {
		return content
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
