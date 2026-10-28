package layoutkraken

import (
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"testing"
)

func TestCombinePreservesEveryLine(t *testing.T) {
	layout := ocrpage.Page{Width: 200, Height: 200, Regions: []ocrpage.Region{{ID: "right", Box: ocrpage.Rect{X1: 100, Y1: 0, X2: 200, Y2: 150}}, {ID: "left", Box: ocrpage.Rect{X1: 0, Y1: 0, X2: 100, Y2: 150}}}}
	lines := ocrpage.FromLines(200, 200, []ocrpage.Line{{ID: "a", Text: "left", Box: ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30}}, {ID: "b", Text: "right", Box: ocrpage.Rect{X1: 110, Y1: 10, X2: 180, Y2: 30}}, {ID: "c", Text: "last", Box: ocrpage.Rect{X1: 0, Y1: 170, X2: 180, Y2: 190}}})
	p, e := Combine(layout, lines)
	if e != nil {
		t.Fatal(e)
	}
	if p.PlainText() != "right\nleft\nlast" || len(p.Lines()) != 3 || len(p.Regions) != 3 {
		t.Fatal(p)
	}
	if len(layout.Lines()) != 0 {
		t.Fatal("mutated layout")
	}
}
