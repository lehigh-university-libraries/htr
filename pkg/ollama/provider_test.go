package ollama

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

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

var _ providers.Client = (*Client)(nil)

func TestRequestOptions(t *testing.T) {
	t.Setenv("OLLAMA_AUDIENCE", "")
	for _, legacy := range []bool{false, true} {
		name := "client"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				numCtx  int
				format  json.RawMessage
				think   *bool
				invalid bool
			}{
				{name: "defaults"},
				{name: "schema without thinking", numCtx: 16384, format: json.RawMessage(`{"type":"string"}`), think: new(bool)},
				{name: "JSON with thinking", format: json.RawMessage(`"json"`), think: boolPointer(true)},
				{name: "negative context", numCtx: -1, invalid: true},
				{name: "invalid schema JSON", format: json.RawMessage(`{"type":`), invalid: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							return
						}
						var options map[string]json.RawMessage
						if err := json.Unmarshal(body["options"], &options); err != nil {
							t.Error(err)
							return
						}
						var numCtx int
						if raw, ok := options["num_ctx"]; ok {
							if err := json.Unmarshal(raw, &numCtx); err != nil {
								t.Error(err)
							}
							if tc.numCtx == 0 {
								t.Error("default num_ctx must be omitted")
							}
						}
						if numCtx != tc.numCtx || string(body["format"]) != string(tc.format) {
							t.Errorf("unexpected context or format: %s", body)
						}
						wantThink := ""
						if tc.think != nil {
							wantThink = "false"
							if *tc.think {
								wantThink = "true"
							}
						}
						if string(body["think"]) != wantThink {
							t.Errorf("think = %s, want %q", body["think"], wantThink)
						}
						_ = json.NewEncoder(w).Encode(map[string]string{"response": `"The image contains text: café"`})
					}))
					defer server.Close()
					request := testRequest([]byte("image"))
					request.NumCtx, request.Format, request.Think = tc.numCtx, tc.format, tc.think
					var text string
					var err error
					if legacy {
						config := providers.Config{BaseURL: server.URL, Model: request.Model, Prompt: request.Prompt, NumCtx: tc.numCtx, Format: tc.format, Think: tc.think}
						text, _, err = New().ExtractText(context.Background(), config, "page.png", base64.StdEncoding.EncodeToString(request.Image.Data))
					} else {
						client, clientErr := NewClient(Options{Endpoint: server.URL})
						if clientErr != nil {
							t.Fatal(clientErr)
						}
						var result providers.Result
						result, err = client.Extract(context.Background(), request)
						text = result.Text
					}
					if tc.invalid {
						var providerError *providers.Error
						if !errors.As(err, &providerError) || providerError.Kind != providers.ErrorInvalidRequest || calls.Load() != 0 {
							t.Fatalf("invalid options: error=%v calls=%d", err, calls.Load())
						}
						return
					}
					wantText := `"The image contains text: café"`
					if len(tc.format) == 0 {
						wantText = providers.CleanResponse(wantText)
					}
					if err != nil || text != wantText || calls.Load() != 1 {
						t.Fatalf("text=%q error=%v calls=%d", text, err, calls.Load())
					}
				})
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

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
