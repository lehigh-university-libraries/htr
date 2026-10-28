package ppdoclayout

import (
	"image"
	"testing"
)

func TestParseLayout(t *testing.T) {
	p, e := ParseLayout([]byte(`{"boxes":[{"label":"text","coordinate":[0,30,90,50],"order":1,"score":0.8},{"label":"doc_title","coordinate":[0,0,90,20],"polygon_points":[[0,0],[90,0],[90,20],[0,20]],"order":0,"score":0.9}]}`), image.Pt(100, 100))
	if e != nil {
		t.Fatal(e)
	}
	if p.Regions[0].SourceType != "doc_title" || len(p.Regions[0].Polygon) != 4 || p.Regions[1].Confidence != .8 {
		t.Fatal(p)
	}
	for _, s := range []string{`{}`, `{"boxes":[{"coordinate":[0,0,10]}]}`, `{"boxes":[{"coordinate":[0,0,1000,10]}]}`, `{"boxes":[{"coordinate":[0,0,10,10],"score":2}]}`} {
		if _, e := ParseLayout([]byte(s), image.Pt(100, 100)); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
