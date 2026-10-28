// Package remote implements the authenticated remote segmentation v2 contract.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/remoteocr"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// Options configures exact endpoints, transport and declared capabilities.
// Capabilities are explicit because Segmenter.Capabilities cannot perform I/O;
// callers may obtain them with FetchCapabilities during registry construction.
type Options struct {
	Endpoint                                         string
	HTTPClient                                       *http.Client
	Authenticator                                    httpclient.Authenticator
	Timeout                                          time.Duration
	MaxImageBytes, MaxRequestBytes, MaxResponseBytes int64
	Capabilities                                     segment.Capabilities
	V1Fallback                                       bool
}

// Client calls v2 operations with optional v1 fallback on HTTP 404/405 only.
type Client struct {
	endpoint, capURL                        string
	http                                    *http.Client
	auth                                    httpclient.Authenticator
	caps                                    segment.Capabilities
	imageLimit, requestLimit, responseLimit int64
	v1                                      *remoteocr.Client
}

// NewClient constructs a bounded client without performing I/O.
func NewClient(o Options) (*Client, error) {
	endpoint, e := httpclient.AppendPath(o.Endpoint, "/v2/segment")
	if e != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	capURL, e := httpclient.AppendPath(o.Endpoint, "/v2/capabilities")
	if e != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if o.MaxImageBytes < 0 || o.MaxRequestBytes < 0 || o.MaxResponseBytes < 0 {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.MaxImageBytes == 0 {
		o.MaxImageBytes = 50 << 20
	}
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 52 << 20
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 64 << 20
	}
	if o.Authenticator == nil {
		o.Authenticator = httpclient.NoAuth{}
	}
	if len(o.Capabilities.Levels) == 0 {
		o.Capabilities.Levels = []ocrpage.Level{ocrpage.LevelLine}
	}
	for _, l := range o.Capabilities.Levels {
		if e := ocrpage.CheckLevel(l); e != nil {
			return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
		}
	}
	o.Capabilities.Levels = append([]ocrpage.Level(nil), o.Capabilities.Levels...)
	c := &Client{endpoint: endpoint, capURL: capURL, http: httpclient.Secure(o.HTTPClient, o.Timeout), auth: o.Authenticator, caps: o.Capabilities, imageLimit: o.MaxImageBytes, requestLimit: o.MaxRequestBytes, responseLimit: o.MaxResponseBytes}
	if o.V1Fallback {
		c.v1, e = remoteocr.NewClient(remoteocr.Options{Endpoint: o.Endpoint, HTTPClient: o.HTTPClient, Authenticator: o.Authenticator, Timeout: o.Timeout, MaxImageBytes: o.MaxImageBytes, MaxRequestBytes: o.MaxRequestBytes, MaxResponseBytes: o.MaxResponseBytes})
		if e != nil {
			return nil, e
		}
	}
	return c, nil
}

// Name returns the registry name.
func (c *Client) Name() string { return "remote" }

// Capabilities returns the declaration configured at construction.
func (c *Client) Capabilities(string) segment.Capabilities {
	v := c.caps
	v.Levels = append([]ocrpage.Level(nil), v.Levels...)
	return v
}
func (c *Client) request(ctx context.Context, req *http.Request) ([]byte, int, error) {
	if e := c.auth.Authorize(ctx, req); e != nil {
		return nil, 0, providers.ErrorForAuthentication(ctx, e)
	}
	res, e := c.http.Do(req)
	if e != nil {
		return nil, 0, providers.ErrorForRequest(ctx, e)
	}
	defer res.Body.Close()
	b, e := httpclient.ReadAll(res.Body, c.responseLimit)
	if e != nil {
		if errors.Is(e, httpclient.ErrResponseTooLarge) {
			return nil, res.StatusCode, providers.NewError(providers.ErrorResponseTooLarge, res.StatusCode, false, nil)
		}
		return nil, res.StatusCode, providers.ErrorForRequest(ctx, e)
	}
	if res.StatusCode != 200 {
		return b, res.StatusCode, providers.ErrorForStatus(res.StatusCode)
	}
	return b, res.StatusCode, nil
}

// FetchCapabilities reads a bounded declaration for one remote model.
func (c *Client) FetchCapabilities(ctx context.Context, model string) (segment.Capabilities, error) {
	u, e := url.Parse(c.capURL)
	if e != nil {
		return segment.Capabilities{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	q := u.Query()
	q.Set("model", model)
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return segment.Capabilities{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	raw, status, e := c.request(ctx, req)
	if e != nil {
		return segment.Capabilities{}, e
	}
	var caps segment.Capabilities
	if e = json.Unmarshal(raw, &caps); e != nil || len(caps.Levels) == 0 {
		return caps, providers.NewError(providers.ErrorInvalidResponse, status, false, nil)
	}
	for _, l := range caps.Levels {
		if e = ocrpage.CheckLevel(l); e != nil {
			return caps, providers.NewError(providers.ErrorInvalidResponse, status, false, nil)
		}
	}
	return caps, nil
}

// Segment uploads an encoded image with model and JSON hints.
func (c *Client) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	if e := providers.ValidateImage(r.Image, c.imageLimit); e != nil {
		return segment.Result{}, e
	}
	if len(r.Model) > 1024 || strings.ContainsAny(r.Model, "\x00\r\n") {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	hint, e := json.Marshal(r.Hints)
	if e != nil || len(hint) > 1<<20 {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	var b bytes.Buffer
	form := multipart.NewWriter(&b)
	media, _ := providers.CanonicalMediaType(r.Image.MediaType)
	ext := ".img"
	switch media {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/tiff":
		ext = ".tif"
	}
	part, e := form.CreateFormFile("image", "image"+ext)
	if e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if _, e = part.Write(r.Image.Data); e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if e = form.WriteField("model", r.Model); e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if e = form.WriteField("hints", string(hint)); e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if e = form.Close(); e != nil || int64(b.Len()) > c.requestLimit {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &b)
	if e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	raw, status, e := c.request(ctx, req)
	if e != nil {
		if c.v1 != nil && (status == 404 || status == 405) {
			result, e := c.v1.Segment(ctx, r.Image, r.Model)
			if e != nil {
				return segment.Result{}, e
			}
			cfg, _, e := image.DecodeConfig(bytes.NewReader(r.Image.Data))
			if e != nil {
				return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
			}
			lines := make([]ocrpage.Line, len(result.Words))
			for i, b := range result.Words {
				lines[i] = ocrpage.Line{Box: ocrpage.Rect{X1: b.X, Y1: b.Y, X2: b.X + b.Width, Y2: b.Y + b.Height}, Text: b.Text, Confidence: b.Confidence}
			}
			p := ocrpage.FromLines(cfg.Width, cfg.Height, lines)
			p.Provenance = ocrpage.Provenance{Provider: result.Provider, Model: r.Model}
			if e = ocrpage.Validate(p, ocrpage.Limits{}); e != nil {
				return segment.Result{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
			}
			native, e := ocrpage.WriteJSON(p)
			if e != nil {
				return segment.Result{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
			}
			return segment.Result{Page: p, Raw: native, RawFormat: "htrjson", EffectiveModel: r.Model}, nil
		}
		return segment.Result{}, e
	}
	p, e := ocrpage.ParseFormat(raw, ocrpage.FormatHTRJSON, ocrpage.ReadOptions{})
	if e != nil || p.Width <= 0 || p.Height <= 0 {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidResponse, status, false, nil)
	}
	return segment.Result{Page: p, Raw: raw, RawFormat: "htrjson", EffectiveModel: r.Model}, nil
}
