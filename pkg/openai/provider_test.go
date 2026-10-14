package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

var _ providers.Client = (*Client)(nil)

func TestClientExtract(t *testing.T) {
	t.Parallel()
	image := []byte("encoded-image")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RawQuery != "" {
			t.Errorf("unexpected request target: %s %s", request.Method, request.URL.String())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body chatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "gpt-4o" || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Fatalf("unexpected chat request: %#v", body)
		}
		if len(body.Messages[0].Content) != 2 || body.Messages[0].Content[0].Text != "Transcribe café" {
			t.Fatalf("unexpected request content: %#v", body.Messages[0].Content)
		}
		wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)
		if got := body.Messages[0].Content[1].ImageURL.URL; got != wantURL {
			t.Errorf("image URL = %q, want %q", got, wantURL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o-2026","choices":[{"message":{"content":"The text in the image reads: café 世界"}}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("test-key")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Extract(context.Background(), testRequest(image))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "café 世界" || result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 4 || result.EffectiveModel != "gpt-4o-2026" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientErrorsAreTypedBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	secretBody := "credential=upstream-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(secretBody))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("private-key")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), testRequest([]byte("image")))
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorRateLimited || !providerError.Retryable {
		t.Fatalf("unexpected error: %#v", err)
	}
	if strings.Contains(err.Error(), secretBody) || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaked sensitive data: %q", err)
	}

	largeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer largeServer.Close()
	limited, err := NewClient(Options{Endpoint: largeServer.URL, APIKey: staticKey("key"), MaxResponseBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	_, err = limited.Extract(context.Background(), testRequest([]byte("image")))
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorResponseTooLarge {
		t.Fatalf("expected response limit error, got %v", err)
	}
}

func TestClientBlocksRedirectWithoutLeakingAuthorization(t *testing.T) {
	t.Parallel()
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		destinationCalls.Add(1)
		if request.Header.Get("Authorization") != "" {
			t.Error("authorization leaked to redirect target")
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer source.Close()
	client, err := NewClient(Options{Endpoint: source.URL, APIKey: staticKey("private-key")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), testRequest([]byte("image")))
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorTransport {
		t.Fatalf("expected transport error, got %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("redirect destination was contacted")
	}
}

func TestClientRejectsInvalidInputBeforeNetwork(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("key"), MaxImageBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest([]byte("12345"))
	_, err = client.Extract(context.Background(), request)
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorInvalidRequest {
		t.Fatalf("expected invalid request, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("network called for invalid input")
	}
}

func TestClientPreservesCancellation(t *testing.T) {
	t.Parallel()
	client, err := NewClient(Options{APIKey: staticKey("key")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Extract(ctx, testRequest([]byte("image")))
	var providerError *providers.Error
	if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation identity, got %v", err)
	}
}

func TestLegacyProviderValidation(t *testing.T) {
	provider := New()
	if provider.Name() != "openai" {
		t.Fatalf("Name() = %q", provider.Name())
	}
	t.Setenv("OPENAI_API_KEY", "")
	if err := provider.ValidateConfig(providers.Config{}); err == nil {
		t.Fatal("expected missing key error")
	}
	t.Setenv("OPENAI_API_KEY", "key")
	if err := provider.ValidateConfig(providers.Config{}); err != nil {
		t.Fatal(err)
	}
}

func testRequest(image []byte) providers.Request {
	return providers.Request{
		Model:       "gpt-4o",
		Prompt:      "Transcribe café",
		Temperature: 0.2,
		Image: providers.Image{
			Data:      image,
			MediaType: "image/png",
			Filename:  "page.png",
		},
	}
}

func staticKey(value string) CredentialSource {
	return func(context.Context) (string, error) { return value, nil }
}

func TestCurrentVisionModels(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-luna", "gpt-4o"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				_, hasTemperature := body["temperature"]
				if hasTemperature != (model == "gpt-4o") {
					t.Errorf("temperature present=%v for %s", hasTemperature, model)
				}
				if model != "gpt-4o" && string(body["reasoning_effort"]) != `"low"` {
					t.Errorf("effort=%s", body["reasoning_effort"])
				}
				if string(body["model"]) != `"`+model+`"` || string(body["max_completion_tokens"]) != "16000" {
					t.Errorf("request=%s", body)
				}
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"café"}}],"usage":{"prompt_tokens":20,"completion_tokens":12,"completion_tokens_details":{"reasoning_tokens":9}}}`))
			}))
			defer server.Close()
			client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("key"), MaxOutputTokens: 16000})
			if err != nil {
				t.Fatal(err)
			}
			request := testRequest([]byte("image"))
			request.Model = model
			result, err := client.Extract(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "café" || result.Usage.OutputTokens != 3 || result.Usage.ThoughtTokens != 9 {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestReasoningOutputLimitRetainsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"prompt_tokens":20,"completion_tokens":12,"completion_tokens_details":{"reasoning_tokens":12}}}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, APIKey: staticKey("key")})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest([]byte("image"))
	request.Model = DefaultModel
	result, err := client.Extract(t.Context(), request)
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) || providerErr.Kind != providers.ErrorOutputLimit || result.Usage.ThoughtTokens != 12 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}
