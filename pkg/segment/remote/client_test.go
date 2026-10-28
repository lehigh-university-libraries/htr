package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestImage(t *testing.T) providers.Image {
	t.Helper()
	var b bytes.Buffer
	if e := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 100, 100))); e != nil {
		t.Fatal(e)
	}
	return providers.Image{Data: b.Bytes(), MediaType: "image/png", Filename: "private-name.png"}
}
func TestV2AndCapabilities(t *testing.T) {
	p := ocrpage.FromLines(100, 100, []ocrpage.Line{{Box: ocrpage.Rect{X1: 0, Y1: 0, X2: 10, Y2: 10}, Text: "été"}})
	wire, e := ocrpage.WriteJSON(p)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if r.URL.Path == "/v2/capabilities" {
			if r.URL.Query().Get("model") != "model" {
				t.Error("model missing")
			}
			if err := json.NewEncoder(w).Encode(segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelLine}}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path != "/v2/segment" {
			t.Error(r.URL.Path)
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				t.Error(err)
			}
		}()
		if r.FormValue("model") != "model" || r.FormValue("hints") != `{"documentType":"letter"}` {
			t.Error("bad fields")
		}
		f, h, e := r.FormFile("image")
		if e != nil {
			t.Error(e)
			return
		}
		f.Close()
		if h.Filename != "image.png" {
			t.Error("private filename sent")
		}
		if _, err := w.Write(wire); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c, e := NewClient(Options{Endpoint: s.URL, Authenticator: httpclient.StaticBearer("secret")})
	if e != nil {
		t.Fatal(e)
	}
	caps, e := c.FetchCapabilities(context.Background(), "model")
	if e != nil || len(caps.Levels) != 1 {
		t.Fatal(caps, e)
	}
	result, e := c.Segment(context.Background(), segment.Request{Image: requestImage(t), Model: "model", Hints: segment.Hints{DocumentType: "letter"}})
	if e != nil || result.Page.PlainText() != "été" {
		t.Fatal(result, e)
	}
}
func TestV1Fallback(t *testing.T) {
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v2/segment" {
			w.WriteHeader(404)
			return
		}
		if _, err := io.WriteString(w, `{"provider":"old","words":[{"X":0,"Y":0,"Width":10,"Height":10,"Text":"old","Confidence":0.9}]}`); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c, e := NewClient(Options{Endpoint: s.URL, V1Fallback: true})
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Segment(context.Background(), segment.Request{Image: requestImage(t)})
	if e != nil || r.Page.PlainText() != "old" || r.Page.Provenance.Provider != "old" || strings.Join(paths, ",") != "/v2/segment,/v1/segment" {
		t.Fatal(r, paths, e)
	}
}
func TestRedactionRedirectAndBounds(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://example.invalid/secret")
				w.WriteHeader(status)
				if _, err := io.WriteString(w, "secret transcription"); err != nil {
					t.Error(err)
				}
			}))
			defer s.Close()
			c, e := NewClient(Options{Endpoint: s.URL})
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.Segment(context.Background(), segment.Request{Image: requestImage(t)})
			if e == nil || strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), s.URL) {
				t.Fatalf("unsafe error %v", e)
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, strings.Repeat("x", 100)); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c, e := NewClient(Options{Endpoint: s.URL, MaxResponseBytes: 10})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Segment(context.Background(), segment.Request{Image: requestImage(t)})
	var pe *providers.Error
	if !errors.As(e, &pe) || pe.Kind != providers.ErrorResponseTooLarge {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = c.Segment(ctx, segment.Request{Image: requestImage(t)})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
