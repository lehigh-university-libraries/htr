package remoteocr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

func TestClientSegmentAndTranscribe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer service-token" {
			t.Errorf("unexpected authorization")
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if request.FormValue("model") != "kraken-base" {
			t.Errorf("model = %q", request.FormValue("model"))
		}
		file, header, err := request.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "encoded-image" || header.Filename != "image.png" {
			t.Errorf("unexpected upload: filename=%q data=%q", header.Filename, data)
		}
		if got := header.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("image content type = %q", got)
		}
		switch request.URL.Path {
		case "/ocr/v1/segment":
			_, _ = w.Write([]byte(`{"provider":"kraken","words":[{"X":1,"Y":2,"Width":3,"Height":4,"Text":"café 世界","Confidence":0.9}]}`))
		case "/ocr/v1/transcribe":
			_, _ = w.Write([]byte(`{"provider":"kraken","model":"kraken-v2","text":"café 世界"}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL + "/ocr", Authenticator: httpclient.StaticBearer("service-token")})
	if err != nil {
		t.Fatal(err)
	}
	image := testImage([]byte("encoded-image"))
	segmented, err := client.Segment(context.Background(), image, "kraken-base")
	if err != nil {
		t.Fatal(err)
	}
	if segmented.Provider != "kraken" || len(segmented.Words) != 1 || segmented.Words[0].Text != "café 世界" {
		t.Fatalf("unexpected segment result: %#v", segmented)
	}
	transcribed, err := client.Transcribe(context.Background(), image, "kraken-base")
	if err != nil {
		t.Fatal(err)
	}
	if transcribed.Provider != "kraken" || transcribed.EffectiveModel != "kraken-v2" || transcribed.Text != "café 世界" {
		t.Fatalf("unexpected transcription result: %#v", transcribed)
	}
}

func TestClientEnforcesRequestAndResponseLimits(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer server.Close()

	imageLimited, err := NewClient(Options{Endpoint: server.URL, MaxImageBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imageLimited.Segment(context.Background(), testImage([]byte("12345")), "model"); errorKind(err) != providers.ErrorInvalidRequest {
		t.Fatalf("expected image limit error, got %v", err)
	}
	requestLimited, err := NewClient(Options{Endpoint: server.URL, MaxImageBytes: 100, MaxRequestBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requestLimited.Segment(context.Background(), testImage([]byte("image")), "model"); errorKind(err) != providers.ErrorInvalidRequest {
		t.Fatalf("expected multipart limit error, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("network called for invalid request")
	}

	responseLimited, err := NewClient(Options{Endpoint: server.URL, MaxResponseBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responseLimited.Transcribe(context.Background(), testImage([]byte("image")), "model"); errorKind(err) != providers.ErrorResponseTooLarge {
		t.Fatalf("expected response limit error, got %v", err)
	}
}

func TestClientRedactsErrorsAndBlocksRedirects(t *testing.T) {
	t.Parallel()
	secret := "private response content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	client, err := NewClient(Options{Endpoint: server.URL, Authenticator: httpclient.StaticBearer("private-token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Segment(context.Background(), testImage([]byte("image")), "model")
	if errorKind(err) != providers.ErrorUpstream {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaked sensitive data: %q", err)
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
	_, err = redirectClient.Segment(context.Background(), testImage([]byte("image")), "model")
	if errorKind(err) != providers.ErrorTransport || destinationCalls.Load() != 0 {
		t.Fatalf("redirect was not safely blocked: error=%v calls=%d", err, destinationCalls.Load())
	}
}

func TestClientRejectsMalformedResponses(t *testing.T) {
	t.Parallel()
	responses := []string{
		`not-json`,
		`{"provider":"kraken","words":[{"X":-1,"Y":0,"Width":1,"Height":1}]}`,
		`{"provider":"kraken","words":[{"X":0,"Y":0,"Width":1,"Height":1,"Confidence":1.5}]}`,
		`{"provider":"","words":[]}`,
	}
	for _, response := range responses {
		response := response
		t.Run(response, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			client, err := NewClient(Options{Endpoint: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Segment(context.Background(), testImage([]byte("image")), "model"); errorKind(err) != providers.ErrorInvalidResponse {
				t.Fatalf("expected invalid response, got %v", err)
			}
		})
	}
}

func TestNewClientRejectsUnsafeEndpoint(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"", "ftp://example.test", "https://user:pass@example.test", "https://example.test?q=secret"} {
		if _, err := NewClient(Options{Endpoint: endpoint}); err == nil {
			t.Errorf("expected %q to be rejected", endpoint)
		}
	}
}

func testImage(data []byte) providers.Image {
	return providers.Image{Data: data, MediaType: "image/png", Filename: "private-document-name.png"}
}

func errorKind(err error) providers.ErrorKind {
	var providerError *providers.Error
	if errors.As(err, &providerError) {
		return providerError.Kind
	}
	return ""
}

func TestFullPageOCRValidation(t *testing.T) {
	const valid = `{"provider":"kraken","model":"catmus","lines":[{"X":10,"Y":20,"Width":100,"Height":30,"Text":"café 世界","Confidence":0.9,"words":[{"X":15,"Y":22,"Width":50,"Height":20,"Text":"café","Confidence":0.8}]}]}`
	for _, tc := range []struct {
		name, response string
		max            int
		invalid        bool
	}{
		{"unicode", valid, 2, false},
		{"outside", strings.Replace(valid, `"X":15`, `"X":5`, 1), 2, true},
		{"box limit", valid, 1, true},
		{"confidence", strings.Replace(valid, `"Confidence":0.8`, `"Confidence":1.1`, 1), 2, true},
		{"empty page", `{"provider":"kraken","model":"catmus","lines":[]}`, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/ocr" || r.Header.Get("Authorization") != "Bearer token" {
					t.Error("incorrect route or authentication")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				if r.FormValue("model") != "catmus" {
					t.Error("model missing")
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			c, err := NewClient(Options{Endpoint: server.URL, Authenticator: httpclient.StaticBearer("token"), MaxBoxes: tc.max})
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.OCR(context.Background(), testImage([]byte("image")), "catmus")
			if (err != nil) != tc.invalid {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if tc.name == "unicode" && result.Lines[0].Text != "café 世界" {
				t.Error("lost unicode")
			}
		})
	}
}
