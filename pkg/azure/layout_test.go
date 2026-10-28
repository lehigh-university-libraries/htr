package azure

import (
	"context"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sampleLayout = `{"status":"succeeded","analyzeResult":{"pages":[{"pageNumber":1,"width":200,"height":100,"unit":"pixel","lines":[{"content":"été 水","polygon":[10,10,110,10,110,30,10,30],"spans":[{"offset":0,"length":5}]}],"words":[{"content":"été","polygon":[10,10,50,10,50,30,10,30],"span":{"offset":0,"length":3},"confidence":0.9},{"content":"水","polygon":[60,10,110,10,110,30,60,30],"span":{"offset":4,"length":1},"confidence":0.95}]}],"paragraphs":[{"role":"title","boundingRegions":[{"pageNumber":1,"polygon":[10,10,110,10,110,30,10,30]}],"spans":[{"offset":0,"length":5}]}]}}`

func TestParseLayout(t *testing.T) {
	p, e := ParseLayout([]byte(sampleLayout), image.Pt(200, 100))
	if e != nil {
		t.Fatal(e)
	}
	if p.PlainText() != "été 水" || p.Regions[0].Type != "heading" || len(p.Words()) != 2 || p.Words()[0].Confidence != .9 {
		t.Fatal(p)
	}
	if _, e = ParseLayout([]byte(sampleLayout), image.Pt(100, 100)); e == nil {
		t.Fatal("accepted wrong dimensions")
	}
}
func TestSharedLayoutPoller(t *testing.T) {
	var server *httptest.Server
	calls := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Ocp-Apim-Subscription-Key") != "secret" {
			t.Error("key missing")
		}
		if r.Method == "POST" {
			if r.URL.Path != "/documentintelligence/documentModels/prebuilt-layout:analyze" || r.URL.Query().Get("api-version") != "2024-11-30" {
				t.Error(r.URL.String())
			}
			body, e := io.ReadAll(r.Body)
			if e != nil || string(body) != "image bytes" {
				t.Error(string(body), e)
			}
			w.Header().Set("Operation-Location", server.URL+"/operation")
			w.WriteHeader(202)
			return
		}
		if _, err := io.WriteString(w, sampleLayout); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	raw, e := analyzeImage(context.Background(), server.URL, "secret", "prebuilt-layout", []byte("image bytes"), time.Second, server.Client(), time.Millisecond)
	if e != nil || string(raw) != sampleLayout || calls != 2 {
		t.Fatal(calls, e)
	}
}

func TestLayoutSpanOrderAndTableParagraphDeduplication(t *testing.T) {
	raw, err := os.ReadFile("testdata/table-middle.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseLayout(raw, image.Pt(200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if p.PlainText() != "Before\nCell\nLoose\nAfter" {
		t.Fatalf("text order: %q", p.PlainText())
	}
	var ids []string
	for _, region := range p.Regions {
		ids = append(ids, region.ID)
	}
	want := []string{"paragraph_1", "table_1", "implicit_3", "figure_1", "paragraph_3"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("regions: %v", ids)
	}
	if len(p.Lines()) != 4 || len(p.Regions[1].Table.Cells[0].LineIDs) != 1 {
		t.Fatal(p)
	}
}

func TestLayoutClampsPageEdgeCoordinates(t *testing.T) {
	raw := strings.ReplaceAll(sampleLayout, "10,10,110,10,110,30,10,30", "-0.5,-0.2,200.5,-0.2,200.5,100.5,-0.5,100.5")
	p, err := ParseLayout([]byte(raw), image.Pt(200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if p.Regions[0].Box != (ocrpage.Rect{X1: 0, Y1: 0, X2: 200, Y2: 100}) {
		t.Fatal(p.Regions[0])
	}
	for _, bad := range []string{"NaN", "1e999"} {
		if _, err = ParseLayout([]byte(strings.Replace(sampleLayout, "10,10", bad+",10", 1)), image.Pt(200, 100)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
