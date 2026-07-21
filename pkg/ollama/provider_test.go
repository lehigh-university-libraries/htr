package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

var _ providers.Client = (*Client)(nil)

func TestClientExtract(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/base/api/generate" || request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected target or authorization")
		}
		var body generateRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "llava" || body.Prompt != "Transcribe café" || len(body.Images) != 1 || body.Stream {
			t.Fatalf("unexpected request: %#v", body)
		}
		_, _ = w.Write([]byte(`{"model":"llava-v2","response":"The image contains text: café 世界","prompt_eval_count":9,"eval_count":3}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL + "/base", Authenticator: httpclient.StaticBearer("token")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Extract(context.Background(), testRequest([]byte("image")))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "café 世界" || result.Usage.InputTokens != 9 || result.Usage.OutputTokens != 3 || result.EffectiveModel != "llava-v2" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientErrorsAreRedactedBoundedAndRedirectSafe(t *testing.T) {
	t.Parallel()
	secret := "secret error body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, Authenticator: httpclient.StaticBearer("private-token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), testRequest([]byte("image")))
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorUpstream || !providerError.Retryable {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaked sensitive data: %q", err)
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer large.Close()
	limited, err := NewClient(Options{Endpoint: large.URL, MaxResponseBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	_, err = limited.Extract(context.Background(), testRequest([]byte("image")))
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorResponseTooLarge {
		t.Fatalf("expected response limit error, got %v", err)
	}

	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer redirect.Close()
	redirectClient, err := NewClient(Options{Endpoint: redirect.URL, Authenticator: httpclient.StaticBearer("private-token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = redirectClient.Extract(context.Background(), testRequest([]byte("image")))
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorTransport || destinationCalls.Load() != 0 {
		t.Fatalf("redirect was not safely blocked: error=%v calls=%d", err, destinationCalls.Load())
	}
}

func TestNewClientRejectsUnsafeEndpoint(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"ftp://example.test", "https://user:pass@example.test", "https://example.test?q=secret"} {
		if _, err := NewClient(Options{Endpoint: endpoint}); err == nil {
			t.Errorf("expected %q to be rejected", endpoint)
		}
	}
}

func TestLegacyConfigurationResolution(t *testing.T) {
	provider := New()
	t.Setenv("OLLAMA_URL", "http://localhost:11434")
	t.Setenv("OLLAMA_AUDIENCE", "")
	if err := provider.ValidateConfig(providers.Config{}); err != nil {
		t.Fatal(err)
	}
	if got := resolveAudience(providers.Config{}, "https://service-abc.run.app"); got != "https://service-abc.run.app" {
		t.Fatalf("auto audience = %q", got)
	}
	if got := resolveAudience(providers.Config{}, "https://example.test"); got != "" {
		t.Fatalf("unexpected audience = %q", got)
	}
}

func testRequest(image []byte) providers.Request {
	return providers.Request{
		Model:       "llava",
		Prompt:      "Transcribe café",
		Temperature: 0.2,
		Image:       providers.Image{Data: image, MediaType: "image/png", Filename: "page.png"},
	}
}
