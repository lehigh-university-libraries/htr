// Package mistral provides the commercial Mistral OCR markdown API client.
package mistral

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// DefaultModel follows Mistral's current OCR alias.
const DefaultModel = "mistral-ocr-latest"

// Options injects explicit transport policy, credentials and resource ceilings.
type Options struct {
	Endpoint                                         string
	APIKey                                           func(context.Context) (string, error)
	HTTPClient                                       *http.Client
	Timeout                                          time.Duration
	MaxImageBytes, MaxRequestBytes, MaxResponseBytes int64
}

// Client returns markdown; it does not declare segmentation capabilities.
type Client struct {
	endpoint                                string
	key                                     func(context.Context) (string, error)
	http                                    *http.Client
	imageLimit, requestLimit, responseLimit int64
}

// NewClient constructs a redirect-rejecting byte-oriented client.
func NewClient(o Options) (*Client, error) {
	if o.Endpoint == "" {
		o.Endpoint = "https://api.mistral.ai/v1/ocr"
	}
	u, e := httpclient.ParseEndpoint(o.Endpoint)
	if e != nil || o.APIKey == nil || o.MaxImageBytes < 0 || o.MaxRequestBytes < 0 || o.MaxResponseBytes < 0 {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.MaxImageBytes == 0 {
		o.MaxImageBytes = 20 << 20
	}
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 28 << 20
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 16 << 20
	}
	return &Client{u.String(), o.APIKey, httpclient.Secure(o.HTTPClient, o.Timeout), o.MaxImageBytes, o.MaxRequestBytes, o.MaxResponseBytes}, nil
}

// Name returns the transcription provider's registry name.
func (c *Client) Name() string { return "mistral-ocr" }

// Extract returns page markdown joined in API reading order. The OCR endpoint
// controls transcription itself and does not accept a transcription prompt.
func (c *Client) Extract(ctx context.Context, r providers.Request) (providers.Result, error) {
	check := r
	if check.Prompt == "" {
		check.Prompt = "OCR"
	}
	if e := providers.ValidateRequest(check, c.imageLimit); e != nil {
		return providers.Result{}, e
	}
	if e := ctx.Err(); e != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, e)
	}
	media, e := providers.CanonicalMediaType(r.Image.MediaType)
	if e != nil {
		return providers.Result{}, e
	}
	key, e := c.key(ctx)
	if e != nil || strings.TrimSpace(key) == "" {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, e)
	}
	payload := struct {
		Model    string `json:"model"`
		Document struct {
			Type     string `json:"type"`
			ImageURL string `json:"image_url"`
		} `json:"document"`
	}{Model: r.Model}
	payload.Document.Type = "image_url"
	payload.Document.ImageURL = "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(r.Image.Data)
	body, e := json.Marshal(payload)
	if e != nil || int64(len(body)) > c.requestLimit {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if e != nil {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	if e = httpclient.StaticBearer(key).Authorize(ctx, req); e != nil {
		return providers.Result{}, providers.ErrorForAuthentication(ctx, e)
	}
	res, e := c.http.Do(req)
	if e != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, e)
	}
	defer res.Body.Close()
	raw, e := httpclient.ReadAll(res.Body, c.responseLimit)
	if e != nil {
		if errors.Is(e, httpclient.ErrResponseTooLarge) {
			return providers.Result{}, providers.NewError(providers.ErrorResponseTooLarge, res.StatusCode, false, nil)
		}
		return providers.Result{}, providers.ErrorForRequest(ctx, e)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return providers.Result{}, providers.ErrorForStatus(res.StatusCode)
	}
	var decoded struct {
		Model string `json:"model"`
		Pages []struct {
			Markdown *string `json:"markdown"`
		} `json:"pages"`
		Usage struct {
			Pages int `json:"pages_processed"`
		} `json:"usage_info"`
	}
	if e = json.Unmarshal(raw, &decoded); e != nil || len(decoded.Pages) == 0 || decoded.Usage.Pages < 0 {
		return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, res.StatusCode, false, nil)
	}
	texts := make([]string, len(decoded.Pages))
	for i, p := range decoded.Pages {
		if p.Markdown == nil {
			return providers.Result{}, providers.NewError(providers.ErrorInvalidResponse, res.StatusCode, false, nil)
		}
		texts[i] = *p.Markdown
	}
	model := decoded.Model
	if model == "" {
		model = r.Model
	}
	return providers.Result{Text: strings.Join(texts, "\n\n"), Usage: providers.UsageInfo{Pages: decoded.Usage.Pages}, EffectiveModel: model}, nil
}

// Provider is the environment-backed CLI adapter.
type Provider struct{}

// New creates a CLI adapter.
func New() *Provider { return &Provider{} }

// Name returns the registry name.
func (p *Provider) Name() string { return "mistral-ocr" }

// ValidateConfig checks the CLI credential without exposing it.
func (p *Provider) ValidateConfig(providers.Config) error {
	if strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")) == "" {
		return providers.NewError(providers.ErrorAuthentication, 0, false, nil)
	}
	return nil
}

// ExtractText adapts the CLI's historical request contract.
func (p *Provider) ExtractText(ctx context.Context, c providers.Config, path, encoded string) (string, providers.UsageInfo, error) {
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Prompt == "" {
		c.Prompt = "OCR"
	}
	r, e := providers.LegacyRequest(c, path, encoded, 20<<20)
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	client, e := NewClient(Options{APIKey: func(context.Context) (string, error) { return os.Getenv("MISTRAL_API_KEY"), nil }, Timeout: c.Timeout})
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	result, e := client.Extract(ctx, r)
	return result.Text, result.Usage, e
}

// CleanResponse preserves markdown exactly for explicit metric normalization.
func (p *Provider) CleanResponse(s string) string { return s }
