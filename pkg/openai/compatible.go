package openai

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// CompatibleProvider adapts explicitly configured OpenAI-compatible endpoints.
// It has no model presets and reuses Client's payload and transport bounds.
type CompatibleProvider struct{}

// NewCompatible creates the CLI adapter for generic endpoints.
func NewCompatible() *CompatibleProvider { return &CompatibleProvider{} }

// Name returns the registry name.
func (p *CompatibleProvider) Name() string { return "openai-compatible" }
func compatibleKeyEnv(c providers.Config) string {
	if c.APIKeyEnv != "" {
		return c.APIKeyEnv
	}
	return "OPENAI_COMPATIBLE_API_KEY"
}

// ValidateConfig requires an explicit endpoint, model, and configured key.
func (p *CompatibleProvider) ValidateConfig(c providers.Config) error {
	if c.BaseURL == "" || strings.TrimSpace(c.Model) == "" {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	u, e := httpclient.ParseEndpoint(c.BaseURL)
	if e != nil {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme != "https" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if strings.TrimSpace(os.Getenv(compatibleKeyEnv(c))) == "" {
		return providers.NewError(providers.ErrorAuthentication, 0, false, nil)
	}
	return nil
}

// ExtractText transcribes using the existing secure byte client.
func (p *CompatibleProvider) ExtractText(ctx context.Context, c providers.Config, path, encoded string) (string, providers.UsageInfo, error) {
	if e := p.ValidateConfig(c); e != nil {
		return "", providers.UsageInfo{}, e
	}
	r, e := providers.LegacyRequest(c, path, encoded, defaultMaxImageBytes)
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	client, e := NewClient(Options{Endpoint: c.BaseURL, APIKey: func(context.Context) (string, error) { return os.Getenv(compatibleKeyEnv(c)), nil }, Timeout: c.Timeout, MaxOutputTokens: c.MaxOutputTokens, ReasoningEffort: c.ReasoningEffort})
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	out, e := client.Extract(ctx, r)
	return out.Text, out.Usage, e
}
