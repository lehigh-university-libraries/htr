// Package remoteocr provides a generic client for bounded multipart OCR services.
package remoteocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

const (
	defaultTimeout          = 2 * time.Minute
	defaultMaxImageBytes    = 50 << 20
	defaultMaxRequestBytes  = 52 << 20
	defaultMaxResponseBytes = 16 << 20
	defaultMaxBoxes         = 100_000
	maxModelBytes           = 256
)

// Options configures a Client. Endpoint must be an exact HTTP(S) service base.
type Options struct {
	HTTPClient       *http.Client
	Endpoint         string
	Authenticator    httpclient.Authenticator
	Timeout          time.Duration
	MaxImageBytes    int64
	MaxRequestBytes  int64
	MaxResponseBytes int64
	MaxBoxes         int
}

// Client calls a remote service's /v1/segment and /v1/transcribe operations.
type Client struct {
	httpClient         *http.Client
	segmentEndpoint    string
	transcribeEndpoint string
	authenticator      httpclient.Authenticator
	maxImageBytes      int64
	maxRequestBytes    int64
	maxResponseBytes   int64
	maxBoxes           int
}

// Box is one OCR region returned by a segmentation service.
type Box struct {
	X          int     `json:"X"`
	Y          int     `json:"Y"`
	Width      int     `json:"Width"`
	Height     int     `json:"Height"`
	Text       string  `json:"Text"`
	Confidence float64 `json:"Confidence"`
}

// SegmentResult contains the selected provider and detected regions.
type SegmentResult struct {
	Provider string `json:"provider"`
	Words    []Box  `json:"words"`
}

// TranscriptionResult contains text and resolved remote model information.
type TranscriptionResult struct {
	Provider       string `json:"provider"`
	EffectiveModel string `json:"model"`
	Text           string `json:"text"`
}

// NewClient constructs a redirect-safe, bounded remote OCR client.
func NewClient(options Options) (*Client, error) {
	segmentEndpoint, err := httpclient.AppendPath(options.Endpoint, "/v1/segment")
	if err != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	transcribeEndpoint, err := httpclient.AppendPath(options.Endpoint, "/v1/transcribe")
	if err != nil {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	authenticator := options.Authenticator
	if authenticator == nil {
		authenticator = httpclient.NoAuth{}
	}
	maxBoxes := options.MaxBoxes
	if maxBoxes <= 0 {
		maxBoxes = defaultMaxBoxes
	}
	return &Client{
		httpClient:         httpclient.Secure(options.HTTPClient, durationOr(options.Timeout, defaultTimeout)),
		segmentEndpoint:    segmentEndpoint,
		transcribeEndpoint: transcribeEndpoint,
		authenticator:      authenticator,
		maxImageBytes:      positiveOr(options.MaxImageBytes, defaultMaxImageBytes),
		maxRequestBytes:    positiveOr(options.MaxRequestBytes, defaultMaxRequestBytes),
		maxResponseBytes:   positiveOr(options.MaxResponseBytes, defaultMaxResponseBytes),
		maxBoxes:           maxBoxes,
	}, nil
}

// Segment detects OCR regions in an encoded image. Model may be empty to use
// the remote service default.
func (c *Client) Segment(ctx context.Context, image providers.Image, model string) (SegmentResult, error) {
	if err := ctx.Err(); err != nil {
		return SegmentResult{}, providers.ErrorForRequest(ctx, err)
	}
	body, contentType, err := c.multipartBody(image, model)
	if err != nil {
		return SegmentResult{}, err
	}
	responseBody, statusCode, err := c.post(ctx, c.segmentEndpoint, body, contentType)
	if err != nil {
		return SegmentResult{}, err
	}
	var result SegmentResult
	if err := json.Unmarshal(responseBody, &result); err != nil || strings.TrimSpace(result.Provider) == "" || len(result.Words) > c.maxBoxes {
		return SegmentResult{}, providers.NewError(providers.ErrorInvalidResponse, statusCode, false, nil)
	}
	for _, box := range result.Words {
		if box.X < 0 || box.Y < 0 || box.Width <= 0 || box.Height <= 0 || box.Confidence < 0 || box.Confidence > 1 ||
			math.IsNaN(box.Confidence) || math.IsInf(box.Confidence, 0) {
			return SegmentResult{}, providers.NewError(providers.ErrorInvalidResponse, statusCode, false, nil)
		}
	}
	result.Provider = strings.TrimSpace(result.Provider)
	return result, nil
}

// Transcribe extracts text from an encoded image. Model may be empty to use
// the remote service default.
func (c *Client) Transcribe(ctx context.Context, image providers.Image, model string) (TranscriptionResult, error) {
	if err := ctx.Err(); err != nil {
		return TranscriptionResult{}, providers.ErrorForRequest(ctx, err)
	}
	body, contentType, err := c.multipartBody(image, model)
	if err != nil {
		return TranscriptionResult{}, err
	}
	responseBody, statusCode, err := c.post(ctx, c.transcribeEndpoint, body, contentType)
	if err != nil {
		return TranscriptionResult{}, err
	}
	var result TranscriptionResult
	if err := json.Unmarshal(responseBody, &result); err != nil || strings.TrimSpace(result.Provider) == "" {
		return TranscriptionResult{}, providers.NewError(providers.ErrorInvalidResponse, statusCode, false, nil)
	}
	result.Provider = strings.TrimSpace(result.Provider)
	result.EffectiveModel = strings.TrimSpace(result.EffectiveModel)
	return result, nil
}

func (c *Client) multipartBody(image providers.Image, model string) (*bytes.Reader, string, error) {
	if err := providers.ValidateImage(image, c.maxImageBytes); err != nil || !validModel(model) {
		return nil, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	mediaType, err := providers.CanonicalMediaType(image.MediaType)
	if err != nil {
		return nil, "", err
	}
	buffer := &boundedBuffer{limit: c.maxRequestBytes}
	writer := multipart.NewWriter(buffer)
	if model != "" {
		if err := writer.WriteField("model", model); err != nil {
			return nil, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="image"; filename="`+uploadFilename(mediaType)+`"`)
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if _, err := part.Write(image.Data); err != nil {
		return nil, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if err := writer.Close(); err != nil {
		return nil, "", providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	return bytes.NewReader(buffer.Bytes()), writer.FormDataContentType(), nil
}

func (c *Client) post(ctx context.Context, endpoint string, body *bytes.Reader, contentType string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, 0, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	request.Header.Set("Content-Type", contentType)
	if err := c.authenticator.Authorize(ctx, request); err != nil {
		return nil, 0, providers.ErrorForAuthentication(ctx, err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, providers.ErrorForRequest(ctx, err)
	}
	defer response.Body.Close()
	responseBody, err := httpclient.ReadAll(response.Body, c.maxResponseBytes)
	if err != nil {
		if errors.Is(err, httpclient.ErrResponseTooLarge) {
			return nil, response.StatusCode, providers.NewError(providers.ErrorResponseTooLarge, response.StatusCode, false, nil)
		}
		return nil, response.StatusCode, providers.ErrorForRequest(ctx, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, response.StatusCode, providers.ErrorForStatus(response.StatusCode)
	}
	return responseBody, response.StatusCode, nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.limit <= 0 || int64(len(data)) > b.limit-int64(b.Len()) {
		return 0, errors.New("multipart request exceeds configured limit")
	}
	return b.Buffer.Write(data)
}

func validModel(model string) bool {
	return len(model) <= maxModelBytes && strings.TrimSpace(model) == model && !strings.ContainsAny(model, "\r\n\x00")
}

func uploadFilename(mediaType string) string {
	parsed, _, _ := mime.ParseMediaType(mediaType)
	switch strings.ToLower(parsed) {
	case "image/jpeg":
		return "image.jpg"
	case "image/png":
		return "image.png"
	case "image/gif":
		return "image.gif"
	case "image/webp":
		return "image.webp"
	case "image/tiff":
		return "image.tiff"
	case "image/jp2":
		return "image.jp2"
	default:
		return "image.img"
	}
}

func positiveOr(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
