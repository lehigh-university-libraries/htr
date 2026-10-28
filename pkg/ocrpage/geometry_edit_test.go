package ocrpage

import (
	"reflect"
	"strings"
	"testing"
)

func TestSVGEditsAndBaselineCuts(t *testing.T) {
	svg := `<svg><polygon points="10,20 110,20 110,60"/><polyline class="baseline" points="10,50 110,55"/></svg>`
	mapped, err := MapSVGPoints(svg, func(p Point) Point { return Point{X: p.X * 2, Y: p.Y + 10} })
	if err != nil || !strings.Contains(mapped, `points="20,60 220,65"`) {
		t.Fatalf("mapped SVG %s %v", mapped, err)
	}
	left, right := SplitBaselineX([]Point{{X: 10, Y: 50}, {X: 110, Y: 60}}, 60)
	if !reflect.DeepEqual(left, []Point{{X: 10, Y: 50}, {X: 60, Y: 55}}) || !reflect.DeepEqual(right, []Point{{X: 60, Y: 55}, {X: 110, Y: 60}}) {
		t.Fatalf("cut %v %v", left, right)
	}
	if _, err := MapSVGPoints(`<svg><polygon points="1,2,3"/></svg>`, func(p Point) Point { return p }); err == nil {
		t.Fatal("accepted incomplete point")
	}
}

func TestIIIFMediaFragmentParameters(t *testing.T) {
	raw := []byte(`{"type":"AnnotationPage","items":[{"id":"line","textGranularity":"line","body":{"type":"TextualBody","value":"checked"},"target":{"source":"https://example.org/canvas","selector":[{"type":"FragmentSelector","value":"t=0,1&track=ocr"},{"type":"FragmentSelector","value":"t=0,1&xywh=pixel:10,20,100,40&track=ocr"}]}}]}`)
	page, err := ReadIIIF(raw, IIIFReadOptions{})
	if err != nil || len(page.Page.Lines()) != 1 || page.Page.Lines()[0].Box != (Rect{X1: 10, Y1: 20, X2: 110, Y2: 60}) {
		t.Fatalf("media fragment %+v %v", page, err)
	}
	if _, err := ParseXYWH("xywh=1,2,3,4&xywh=5,6,7,8"); err == nil {
		t.Fatal("accepted ambiguous spatial fragment")
	}
}
