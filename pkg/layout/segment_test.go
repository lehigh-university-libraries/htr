package layout

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/remoteocr"
)

func TestSegmentTemplate(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 200, 100))); err != nil {
		t.Fatal(err)
	}
	page := providers.Image{Data: encoded.Bytes(), MediaType: "image/png", Filename: `page&1.png`}
	cases := []struct {
		name    string
		boxes   []remoteocr.Box
		wantErr bool
	}{
		{"service order", []remoteocr.Box{{X: 110, Y: 10, Width: 50, Height: 20}, {X: 10, Y: 10, Width: 50, Height: 20}}, false},
		{"empty", nil, true},
		{"outside page", []remoteocr.Box{{X: 190, Y: 10, Width: 20, Height: 20}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/segment" || r.Header.Get("Authorization") != "Bearer test-identity" {
					t.Error("wrong segmentation route or identity")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer func() {
					if err := r.MultipartForm.RemoveAll(); err != nil {
						t.Error(err)
					}
				}()
				if r.FormValue("model") != "newspapers" {
					t.Error("wrong model")
				}
				if err := json.NewEncoder(w).Encode(remoteocr.SegmentResult{Provider: "newspapers", Words: tc.boxes}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := remoteocr.NewClient(remoteocr.Options{Endpoint: server.URL, Authenticator: httpclient.StaticBearer("test-identity")})
			if err != nil {
				t.Fatal(err)
			}
			template, err := Segment(t.Context(), client, page, "newspapers")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if calls != 1 {
				t.Fatalf("segment requests=%d", calls)
			}
			if tc.wantErr {
				return
			}
			if !strings.Contains(string(template), `imageFilename="page&amp;1.png"`) || !strings.Contains(string(template), `imageWidth="200" imageHeight="100"`) {
				t.Fatalf("wrong page metadata: %s", template)
			}
			result, err := Merge(template, FormatPageXML, `<PcGts><TextLine id="line_2"><Coords points="0,0"/><TextEquiv><Unicode>Second</Unicode></TextEquiv></TextLine><TextLine id="line_1"><Coords points="110,10 160,10 160,30 110,30"/><TextEquiv><Unicode>First</Unicode></TextEquiv></TextLine></PcGts>`)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "First\nSecond" || result.Filled != 2 || len(result.GeometryChanged) != 1 {
				t.Fatalf("text=%q filled=%d geometryChanged=%v", result.Text, result.Filled, result.GeometryChanged)
			}
			if !strings.Contains(string(result.Document), `points="110,10 160,10 160,30 110,30"`) {
				t.Fatalf("service geometry lost: %s", result.Document)
			}
		})
	}
}
