package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

func TestPayloadAndMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("bad authentication or method")
		}
		var payload map[string]any
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Error(e)
		}
		doc, ok := payload["document"].(map[string]any)
		if !ok || doc["type"] != "image_url" || doc["image_url"] != "data:image/png;base64,YWJj" || payload["model"] != DefaultModel || len(payload) != 2 {
			t.Errorf("payload %#v", payload)
		}
		if _, err := io.WriteString(w, `{"model":"resolved","pages":[{"markdown":"# été"},{"markdown":"水"}],"usage_info":{"pages_processed":2}}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, e := NewClient(Options{Endpoint: server.URL, APIKey: func(context.Context) (string, error) { return "secret", nil }})
	if e != nil {
		t.Fatal(e)
	}
	r, e := client.Extract(context.Background(), providers.Request{Model: DefaultModel, Image: providers.Image{Data: []byte("abc"), MediaType: "image/png"}})
	if e != nil || r.Text != "# été\n\n水" || r.Usage.Pages != 2 || r.EffectiveModel != "resolved" {
		t.Fatal(r, e)
	}
}
func TestFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		limit  int64
		kind   providers.ErrorKind
	}{
		{"status", 429, "private transcription secret", 0, providers.ErrorRateLimited},
		{"oversize", 200, strings.Repeat("x", 100), 10, providers.ErrorResponseTooLarge},
		{"missing", 200, `{"pages":[{}]}`, 0, providers.ErrorInvalidResponse},
		{"malformed", 200, "not JSON", 0, providers.ErrorInvalidResponse},
		{"redirect", 302, "secret", 0, providers.ErrorTransport},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.status == 302 {
					w.Header().Set("Location", "http://example.invalid/private")
				}
				w.WriteHeader(tt.status)
				if _, err := io.WriteString(w, tt.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			c, e := NewClient(Options{Endpoint: server.URL, APIKey: func(context.Context) (string, error) { return "secret", nil }, MaxResponseBytes: tt.limit})
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.Extract(context.Background(), providers.Request{Model: DefaultModel, Image: providers.Image{Data: []byte("abc"), MediaType: "image/png"}})
			var pe *providers.Error
			if !errors.As(e, &pe) || pe.Kind != tt.kind {
				t.Fatalf("%v", e)
			}
			if strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), server.URL) {
				t.Fatalf("leaked error: %v", e)
			}
		})
	}
}
func TestBoundsAndCancellation(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer s.Close()
	c, e := NewClient(Options{Endpoint: s.URL, MaxImageBytes: 2, APIKey: func(context.Context) (string, error) { return "key", nil }})
	if e != nil {
		t.Fatal(e)
	}
	r := providers.Request{Model: DefaultModel, Image: providers.Image{Data: []byte("abc"), MediaType: "image/png"}}
	if _, e = c.Extract(context.Background(), r); e == nil {
		t.Fatal("accepted oversized image")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Image.Data = []byte("a")
	if _, e = c.Extract(ctx, r); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatal("made invalid request")
	}
}
