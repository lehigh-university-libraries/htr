package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

var _ providers.Client = (*Client)(nil)

func TestClientExtractUsesHeaderCredentialAndIgnoresThoughts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models/gemini-2.5-pro:generateContent" || request.URL.RawQuery != "" {
			t.Errorf("unexpected request URL: %s", request.URL.String())
		}
		if got := request.Header.Get("x-goog-api-key"); got != "gemini-key" {
			t.Errorf("API key header = %q", got)
		}
		var body generateRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 || body.Contents[0].Parts[0].Text != "Transcribe café" {
			t.Fatalf("unexpected request: %#v", body)
		}
		_, _ = w.Write([]byte(`{"modelVersion":"gemini-2.5-pro-002","candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"private reasoning","thought":true},{"text":"Here's the text from the image: café 世界"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3}}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("gemini-key"), IncludeThoughts: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Extract(context.Background(), testRequest([]byte("image")))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "café 世界" || result.Usage.InputTokens != 8 || result.Usage.OutputTokens != 3 || result.EffectiveModel != "gemini-2.5-pro-002" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientResolutionFallback(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var resolutions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body generateRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		resolutions = append(resolutions, body.GenerationConfig.MediaResolution)
		attempt := len(resolutions)
		mu.Unlock()
		if attempt == 1 {
			_, _ = w.Write([]byte(`{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"partial"}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"complete"}]}}]}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{
		Endpoint: server.URL, APIKey: staticKey("key"),
		MediaResolution: "MEDIA_RESOLUTION_HIGH", MediaResolutionFallback: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Extract(context.Background(), testRequest([]byte("image")))
	if err != nil || result.Text != "complete" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(resolutions) != 2 || resolutions[0] != "MEDIA_RESOLUTION_HIGH" || resolutions[1] != "MEDIA_RESOLUTION_MEDIUM" {
		t.Fatalf("resolution attempts = %#v", resolutions)
	}
}

func TestClientErrorsAreRedactedAndRedirectSafe(t *testing.T) {
	t.Parallel()
	secret := "sensitive-upstream-response"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("private-key")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), testRequest([]byte("image")))
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorAuthentication {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaked sensitive data: %q", err)
	}

	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer redirect.Close()
	redirectClient, err := NewClient(Options{Endpoint: redirect.URL, APIKey: staticKey("private-key")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = redirectClient.Extract(context.Background(), testRequest([]byte("image")))
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorTransport || destinationCalls.Load() != 0 {
		t.Fatalf("redirect was not safely blocked: error=%v calls=%d", err, destinationCalls.Load())
	}
}

func TestNewClientRejectsInvalidResolution(t *testing.T) {
	t.Parallel()
	if _, err := NewClient(Options{APIKey: staticKey("key"), MediaResolution: "arbitrary"}); err == nil {
		t.Fatal("expected invalid resolution error")
	}
}

func TestLegacyProviderValidation(t *testing.T) {
	provider := New()
	if provider.Name() != "gemini" {
		t.Fatalf("Name() = %q", provider.Name())
	}
	t.Setenv("GEMINI_API_KEY", "")
	if err := provider.ValidateConfig(providers.Config{}); err == nil {
		t.Fatal("expected missing key error")
	}
	t.Setenv("GEMINI_API_KEY", "key")
	if err := provider.ValidateConfig(providers.Config{}); err != nil {
		t.Fatal(err)
	}
}

func testRequest(image []byte) providers.Request {
	return providers.Request{
		Model:       "gemini-2.5-pro",
		Prompt:      "Transcribe café",
		Temperature: 0.2,
		Image:       providers.Image{Data: image, MediaType: "image/png", Filename: "page.png"},
	}
}

func staticKey(value string) CredentialSource {
	return func(context.Context) (string, error) { return value, nil }
}
