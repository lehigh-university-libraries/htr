package segeval

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	yaml "go.yaml.in/yaml/v3"
)

func page(boxes ...ocrpage.Rect) ocrpage.Page {
	var lines []ocrpage.Line
	for _, b := range boxes {
		lines = append(lines, ocrpage.Line{Box: b})
	}
	return ocrpage.FromLines(200, 200, lines)
}
func closeTo(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %g want %g", got, want)
	}
}
func TestErrors(t *testing.T) {
	a, b := ocrpage.Rect{X1: 10, Y1: 10, X2: 50, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 40, X2: 50, Y2: 60}
	for _, tt := range []struct {
		name    string
		gt, hyp ocrpage.Page
		err     string
	}{
		{"miss", page(a, b), page(a), "miss"},
		{"partial", page(a), page(ocrpage.Rect{X1: 10, Y1: 10, X2: 40, Y2: 30}), "partial"},
		{"false", page(a), page(a, b), "false_detection"},
		{"mergeV", page(a, b), page(ocrpage.Rect{X1: 10, Y1: 10, X2: 50, Y2: 60}), "merge_v"},
		{"mergeH", page(a, ocrpage.Rect{X1: 60, Y1: 10, X2: 100, Y2: 30}), page(ocrpage.Rect{X1: 10, Y1: 10, X2: 100, Y2: 30}), "merge_h"},
		{"split", page(a), page(ocrpage.Rect{X1: 10, Y1: 10, X2: 30, Y2: 30}, ocrpage.Rect{X1: 30, Y1: 10, X2: 50, Y2: 30}), "split"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, e := Evaluate(tt.gt, tt.hyp, Options{})
			if e != nil {
				t.Fatal(e)
			}
			for name, v := range r.PRImA.Errors {
				if name == tt.err {
					if v.Count != 1 {
						t.Fatalf("wanted one %s, got %+v", name, v)
					}
				} else if v.Count != 0 {
					t.Fatalf("unexpected %s: %+v", name, v)
				}
			}
		})
	}
}
func TestIdentityProfilesAndScale(t *testing.T) {
	p := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 50, X2: 80, Y2: 70})
	for _, profile := range Profiles() {
		t.Run(profile.Name, func(t *testing.T) {
			r, e := Evaluate(p, p.Scale(400, 400), Options{Profile: profile})
			if e != nil {
				t.Fatal(e)
			}
			closeTo(t, r.Score, 1)
			closeTo(t, r.PRImA.Score, 1)
			closeTo(t, r.MOTe.Total, 0)
			closeTo(t, r.Matching.MeanIoU, 1)
			closeTo(t, r.Matching.MeanF1, 1)
			closeTo(t, r.ReadingOrder, 1)
			if profile.Primary == MeasureCBAD && r.FallbackReason == "" {
				t.Fatal("missing baseline fallback")
			}
		})
	}
}
func TestPolygonAndOrder(t *testing.T) {
	p := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 50, X2: 80, Y2: 70})
	for i := range p.Regions[0].Lines {
		p.Regions[0].Lines[i].Polygon = p.Regions[0].Lines[i].Box.Polygon()
	}
	r, e := Evaluate(p, p, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if r.GeometryMode != "polygon" {
		t.Fatal(r.GeometryMode)
	}
	closeTo(t, r.Score, 1)
	q := p.Scale(200, 200)
	q.Regions[0].Lines[0], q.Regions[0].Lines[1] = q.Regions[0].Lines[1], q.Regions[0].Lines[0]
	r, e = Evaluate(p, q, Options{})
	if e != nil {
		t.Fatal(e)
	}
	closeTo(t, r.ReadingOrder, 0)
	closeTo(t, r.Score, 0)
}
func TestBaselines(t *testing.T) {
	p := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 110, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 50, X2: 110, Y2: 70})
	for i := range p.Regions[0].Lines {
		l := &p.Regions[0].Lines[i]
		l.Baseline = []ocrpage.Point{{X: l.Box.X1, Y: l.Box.Y2 - 5}, {X: l.Box.X2, Y: l.Box.Y2 - 5}}
	}
	profile, _ := Lookup("letter")
	r, e := Evaluate(p, p, Options{Profile: profile})
	if e != nil {
		t.Fatal(e)
	}
	if r.CBAD == nil || r.Primary != "cbad" {
		t.Fatal(r)
	}
	closeTo(t, r.Score, 1)
	closeTo(t, r.CBAD.Chamfer, 0)
	q := p.Scale(200, 200)
	for i := range q.Regions[0].Lines {
		for j := range q.Regions[0].Lines[i].Baseline {
			q.Regions[0].Lines[i].Baseline[j].Y += 20
		}
	}
	r, e = Evaluate(p, q, Options{Profile: profile})
	if e != nil {
		t.Fatal(e)
	}
	closeTo(t, r.CBAD.F, .5)
	p.Regions[0].Lines[0].BaselineDerived = true
	r, e = Evaluate(p, p, Options{Profile: profile})
	if e != nil || r.FallbackReason == "" {
		t.Fatal(r, e)
	}
}
func TestMatchingAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for n := 1; n <= 7; n++ {
		for m := 1; m <= 7; m++ {
			for rep := 0; rep < 5; rep++ {
				w := make([][]float64, n)
				for i := range w {
					w[i] = make([]float64, m)
					for j := range w[i] {
						w[i][j] = float64(rng.Intn(10)) / 10
					}
				}
				got := 0.0
				for _, p := range assign(w) {
					got += p.iou
				}
				var best func(int, int) float64
				best = func(i, mask int) float64 {
					if i == n {
						return 0
					}
					v := best(i+1, mask)
					for j, x := range w[i] {
						if mask&(1<<j) == 0 {
							v = max(v, x+best(i+1, mask|1<<j))
						}
					}
					return v
				}
				closeTo(t, got, best(0, 0))
			}
		}
	}
}
func TestProfiles(t *testing.T) {
	if len(Profiles()) < 7 {
		t.Fatal("missing profiles")
	}
	p, _ := Lookup("newspaper")
	data, e := yaml.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	q, e := LoadProfile(strings.NewReader(string(data)))
	if e != nil || !reflect.DeepEqual(p, q) {
		t.Fatal(string(data), q, e)
	}
	q.Ignore[0] = ocrpage.RegionNoise
	again, _ := Lookup("newspaper")
	if again.Ignore[0] != ocrpage.RegionImage {
		t.Fatal("registry leaked mutable state")
	}
	p.Weights.Miss = -1
	if Register(p) == nil {
		t.Fatal("negative weight accepted")
	}
}
func TestMOTeFixtures(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/segeval/mote-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name                               string
			GroundTruth, Hypothesis            [][]int
			Missing, Overlap, Trespass, Excess float64
		}
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	toPage := func(boxes [][]int) ocrpage.Page {
		var b []ocrpage.Rect
		for _, v := range boxes {
			b = append(b, ocrpage.Rect{X1: v[0], Y1: v[1], X2: v[0] + v[2], Y2: v[1] + v[3]})
		}
		return page(b...)
	}
	for _, f := range fixtures.Cases {
		t.Run(f.Name, func(t *testing.T) {
			r, e := Evaluate(toPage(f.GroundTruth), toPage(f.Hypothesis), Options{})
			if e != nil {
				t.Fatal(e)
			}
			closeTo(t, r.MOTe.Missing, f.Missing)
			closeTo(t, r.MOTe.Overlap, f.Overlap)
			closeTo(t, r.MOTe.Trespass, f.Trespass)
			closeTo(t, r.MOTe.Excess, f.Excess)
		})
	}
}

func TestMisclassificationAndIgnore(t *testing.T) {
	p := ocrpage.Page{Width: 200, Height: 200, Regions: []ocrpage.Region{{Type: ocrpage.RegionText, Box: ocrpage.Rect{X1: 0, Y1: 0, X2: 50, Y2: 50}}}}
	q := p.Scale(200, 200)
	q.Regions[0].Type = ocrpage.RegionHeading
	r, e := Evaluate(p, q, Options{Level: ocrpage.LevelRegion})
	if e != nil {
		t.Fatal(e)
	}
	if r.PRImA.Errors["misclassification"].Count != 1 {
		t.Fatal(r)
	}
	r, e = Evaluate(p, p, Options{Level: ocrpage.LevelRegion, Ignore: []ocrpage.RegionType{ocrpage.RegionText}})
	if e != nil {
		t.Fatal(e)
	}
	closeTo(t, r.Score, 1)
}
func TestCBADReference(t *testing.T) {
	data, e := os.ReadFile("../../fixtures/segeval/cbad-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures struct {
		Cases []struct {
			Name                    string
			GroundTruth, Hypothesis [][][]int
			Precision, Recall, F    float64
		}
	}
	if e = json.Unmarshal(data, &fixtures); e != nil {
		t.Fatal(e)
	}
	elements := func(lines [][][]int) []ocrpage.Element {
		var out []ocrpage.Element
		for _, line := range lines {
			var p []ocrpage.Point
			for _, pt := range line {
				p = append(p, ocrpage.Point{X: pt[0], Y: pt[1]})
			}
			out = append(out, ocrpage.Element{Baseline: p})
		}
		return out
	}
	for _, f := range fixtures.Cases {
		t.Run(f.Name, func(t *testing.T) {
			got := baselineScore(elements(f.GroundTruth), elements(f.Hypothesis))
			if math.Abs(got.Precision-f.Precision) > 1e-3 || math.Abs(got.Recall-f.Recall) > 1e-3 || math.Abs(got.F-f.F) > 1e-3 {
				t.Fatalf("reference P/R/F=%g/%g/%g; got %+v", f.Precision, f.Recall, f.F, got)
			}
		})
	}
}
func TestEmptyPrediction(t *testing.T) {
	gt := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 50, Y2: 30})
	r, e := Evaluate(gt, page(), Options{})
	if e != nil {
		t.Fatal(e)
	}
	closeTo(t, r.Score, 0)
	closeTo(t, r.MOTe.Missing, 1)
	if r.PRImA.Errors["miss"].Count != 1 {
		t.Fatal(r)
	}
	empty := page()
	r, e = Evaluate(empty, empty, Options{})
	if e != nil {
		t.Fatal(e)
	}
	closeTo(t, r.Score, 1)
}
func TestTableColumns(t *testing.T) {
	gt := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 50, Y2: 30}, ocrpage.Rect{X1: 110, Y1: 10, X2: 150, Y2: 30})
	gt.Regions[0].Table = &ocrpage.Table{Rows: 1, Cols: 2, Cells: []ocrpage.Cell{{RowSpan: 1, ColSpan: 1, Box: ocrpage.Rect{X1: 0, Y1: 0, X2: 100, Y2: 40}}, {Col: 1, RowSpan: 1, ColSpan: 1, Box: ocrpage.Rect{X1: 100, Y1: 0, X2: 200, Y2: 40}}}}
	hyp := gt.Scale(200, 200)
	hyp.Regions[0].Lines[0].Box.X2 = 90
	p, _ := Lookup("ledger")
	r, e := Evaluate(gt, hyp, Options{Profile: p})
	if e != nil {
		t.Fatal(e)
	}
	if r.Columns == nil {
		t.Fatal("no columns")
	}
	closeTo(t, *r.Columns, 1)
}

func TestHandwritingWithoutHypothesisBaselinesUsesGeometry(t *testing.T) {
	gt := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 50, X2: 80, Y2: 70})
	for i := range gt.Regions[0].Lines {
		l := &gt.Regions[0].Lines[i]
		l.Baseline = []ocrpage.Point{{X: l.Box.X1, Y: l.Box.Y2 - 2}, {X: l.Box.X2, Y: l.Box.Y2 - 2}}
	}
	hyp := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30}, ocrpage.Rect{X1: 10, Y1: 50, X2: 80, Y2: 70})
	for _, name := range []string{"letter", "manuscript"} {
		profile, _ := Lookup(name)
		r, err := Evaluate(gt, hyp, Options{Profile: profile})
		if err != nil || r.Score != 1 || r.Primary != "prima" || r.FallbackReason != "hypothesis has no baselines" || r.CBAD != nil {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		partial := hyp.Scale(200, 200)
		partial.Regions[0].Lines[0].Baseline = gt.Regions[0].Lines[0].Baseline
		r, err = Evaluate(gt, partial, Options{Profile: profile})
		if err != nil || r.Primary != "cbad" || r.Score >= 1 {
			t.Fatalf("partial baselines: %+v %v", r, err)
		}
	}
}

func TestCollapsedGeometryIsCountedAndDropped(t *testing.T) {
	gt := page(ocrpage.Rect{X1: 10, Y1: 10, X2: 80, Y2: 30})
	hyp := page(ocrpage.Rect{X1: 100, Y1: 100, X2: 800, Y2: 300}, ocrpage.Rect{X1: 1000, Y1: 1000, X2: 1001, Y2: 1001})
	hyp.Width, hyp.Height = 2000, 2000
	r, err := Evaluate(gt, hyp, Options{})
	if err != nil || r.Dropped.Hypothesis != 1 || r.Counts.Hypothesis != 1 || r.Score != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestReportYAMLUsesStableSnakeCase(t *testing.T) {
	r := Report{FallbackReason: "missing", GeometryMode: "box", PRImA: &PRImAResult{Errors: map[string]ErrorResult{"miss": {AreaRate: 0.5}}}, CBAD: &CBADResult{ChamferDefined: true}}
	b, err := yaml.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fallback_reason:", "geometry_mode:", "area_rate:", "chamfer_defined:"} {
		if !strings.Contains(string(b), key) {
			t.Fatal(string(b))
		}
	}
	var decoded Report
	if err = yaml.Unmarshal(b, &decoded); err != nil || !reflect.DeepEqual(r, decoded) {
		t.Fatalf("%+v %v", decoded, err)
	}
}
