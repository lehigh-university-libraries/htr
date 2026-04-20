package ollama

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Provider implements the Ollama local provider
type Provider struct{}

type cachedIdentityToken struct {
	token     string
	expiresAt time.Time
}

var (
	metadataIdentityTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"
	identityTokenCacheMu     sync.Mutex
	identityTokenCache       = map[string]cachedIdentityToken{}
)

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
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	ollamaURL := resolveBaseURL(config)

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
		return "", providers.UsageInfo{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	url := fmt.Sprintf("%s/api/generate", strings.TrimSuffix(ollamaURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", providers.UsageInfo{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	if audience := resolveAudience(config, ollamaURL); audience != "" {
		token, err := identityToken(ctx, audience)
		if err != nil {
			return "", providers.UsageInfo{}, fmt.Errorf("fetch identity token for ollama audience %q: %w", audience, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
		return "", providers.UsageInfo{}, fmt.Errorf("ollama API error: %d - %s", resp.StatusCode, string(body))
	}

	var ollamaResp map[string]interface{}
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return "", providers.UsageInfo{}, fmt.Errorf("failed to parse JSON response: %w - body: %s", err, providers.TruncateBody(body))
	}

	// Extract response from Ollama
	response, ok := ollamaResp["response"].(string)
	if !ok {
		return "", providers.UsageInfo{}, fmt.Errorf("no response from Ollama - body: %s", providers.TruncateBody(body))
	}

	// Extract token usage if available (Ollama provides prompt_eval_count and eval_count)
	usage := providers.UsageInfo{}
	if promptEvalCount, ok := ollamaResp["prompt_eval_count"].(float64); ok {
		usage.InputTokens = int(promptEvalCount)
	}
	if evalCount, ok := ollamaResp["eval_count"].(float64); ok {
		usage.OutputTokens = int(evalCount)
	}

	return providers.ProcessResponse(p, response), usage, nil
}

func resolveBaseURL(config providers.Config) string {
	if baseURL := strings.TrimSpace(config.BaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	if envURL := strings.TrimSpace(os.Getenv("OLLAMA_URL")); envURL != "" {
		return strings.TrimRight(envURL, "/")
	}
	return "http://localhost:11434"
}

func resolveAudience(config providers.Config, baseURL string) string {
	if audience := strings.TrimSpace(config.Audience); audience != "" {
		return audience
	}
	if envAudience := strings.TrimSpace(os.Getenv("OLLAMA_AUDIENCE")); envAudience != "" {
		return envAudience
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return ""
	}

	host := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(host, ".run.app") {
		return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}
	return ""
}

func identityToken(ctx context.Context, audience string) (string, error) {
	identityTokenCacheMu.Lock()
	if cached, ok := identityTokenCache[audience]; ok && time.Until(cached.expiresAt) > 2*time.Minute {
		identityTokenCacheMu.Unlock()
		return cached.token, nil
	}
	identityTokenCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataIdentityTokenURL, nil)
	if err != nil {
		return "", err
	}
	query := req.URL.Query()
	query.Set("audience", audience)
	query.Set("format", "full")
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read metadata response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server error: %d - %s", resp.StatusCode, providers.TruncateBody(body))
	}

	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", fmt.Errorf("metadata server returned an empty identity token")
	}
	expiry, err := identityTokenExpiry(token)
	if err != nil {
		expiry = time.Now().Add(45 * time.Minute)
	}

	identityTokenCacheMu.Lock()
	identityTokenCache[audience] = cachedIdentityToken{
		token:     token,
		expiresAt: expiry,
	}
	identityTokenCacheMu.Unlock()
	return token, nil
}

func identityTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode jwt payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse jwt payload: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("jwt missing exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}
