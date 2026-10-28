package segeval

import (
	"errors"
	"fmt"
	"slices"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
)

// Options overrides a profile's level and ignored canonical types.
type Options struct {
	Profile Profile
	Level   ocrpage.Level
	Ignore  []ocrpage.RegionType
}

// ErrorResult records raw error mass and weighted error rates.
type ErrorResult struct {
	Count     int     `yaml:"count"`
	Area      float64 `yaml:"area"`
	AreaRate  float64 `yaml:"area_rate"`
	CountRate float64 `yaml:"count_rate"`
	Success   float64 `yaml:"success"`
}

// PRImAResult reports directional merge, split and detection errors.
type PRImAResult struct {
	Errors     map[string]ErrorResult `yaml:"errors"`
	Score      float64                `yaml:"score"`
	CountScore float64                `yaml:"count_score"`
}

// CBADResult reports baseline precision, recall, F and bidirectional Chamfer distance.
type CBADResult struct {
	Precision float64 `yaml:"precision"`
	Recall    float64 `yaml:"recall"`
	F         float64 `yaml:"f"`
	Chamfer   float64 `yaml:"chamfer"`
	// ChamferDefined is false when either side has no baseline points.
	ChamferDefined bool `yaml:"chamfer_defined"`
}

// MOTeResult reports missing, overlapping, trespassing and excess geometry.
type MOTeResult struct {
	Missing  float64 `yaml:"missing"`
	Overlap  float64 `yaml:"overlap"`
	Trespass float64 `yaml:"trespass"`
	Total    float64 `yaml:"total"`
	Excess   float64 `yaml:"excess"`
}

// PRF contains precision, recall and F1 at an IoU threshold.
type PRF struct {
	Precision float64 `yaml:"precision"`
	Recall    float64 `yaml:"recall"`
	F1        float64 `yaml:"f1"`
}

// MatchingResult reports one-to-one geometry agreement.
type MatchingResult struct {
	At50    PRF     `yaml:"at50"`
	At75    PRF     `yaml:"at75"`
	MeanF1  float64 `yaml:"mean_f1"`
	MeanIoU float64 `yaml:"mean_iou"`
	Matched int     `yaml:"matched"`
}

// Counts records retained elements after type filtering.
type Counts struct {
	GroundTruth int `yaml:"ground_truth"`
	Hypothesis  int `yaml:"hypothesis"`
}

// Report contains the selected primary score and every computed component.
type Report struct {
	Profile        string          `yaml:"profile"`
	Level          string          `yaml:"level"`
	GeometryMode   string          `yaml:"geometry_mode"`
	Score          float64         `yaml:"score"`
	Primary        string          `yaml:"primary"`
	FallbackReason string          `yaml:"fallback_reason"`
	Calibrated     bool            `yaml:"calibrated"`
	PRImA          *PRImAResult    `yaml:"prima"`
	PRImARegion    *PRImAResult    `yaml:"prima_region"`
	CBAD           *CBADResult     `yaml:"cbad"`
	MOTe           *MOTeResult     `yaml:"mote"`
	Matching       *MatchingResult `yaml:"matching"`
	ReadingOrder   float64         `yaml:"reading_order"`
	Columns        *float64        `yaml:"columns"`
	Counts         Counts          `yaml:"counts"`
	Dropped        Counts          `yaml:"dropped"`
}

// Evaluate compares pages after scaling hypothesis geometry to ground truth.
func Evaluate(gt, hyp ocrpage.Page, o Options) (Report, error) {
	var out Report
	if gt.Width <= 0 || gt.Height <= 0 {
		return out, errors.New("segeval: ground truth lacks dimensions")
	}
	if hyp.Width <= 0 || hyp.Height <= 0 {
		return out, errors.New("segeval: hypothesis lacks dimensions")
	}
	if err := ocrpage.Validate(gt, ocrpage.Limits{}); err != nil {
		return out, fmt.Errorf("segeval: ground truth: %w", err)
	}
	if err := ocrpage.Validate(hyp, ocrpage.Limits{}); err != nil {
		return out, fmt.Errorf("segeval: hypothesis: %w", err)
	}
	p := o.Profile
	if p.Name == "" {
		p, _ = Lookup("generic")
	}
	if err := validProfile(p); err != nil {
		return out, err
	}
	level := o.Level
	if level == "" {
		level = p.Level
	}
	if err := ocrpage.CheckLevel(level); err != nil {
		return out, err
	}
	hyp = hyp.Scale(gt.Width, gt.Height)
	filter := func(v []ocrpage.Element) []ocrpage.Element {
		return slices.DeleteFunc(v, func(e ocrpage.Element) bool {
			return slices.Contains(p.Ignore, e.Type) || slices.Contains(o.Ignore, e.Type)
		})
	}
	gs, hs := filter(gt.Elements(level)), filter(hyp.Elements(level))
	if len(gs) == 0 && len(gt.Elements(level)) == 0 && hasOtherGeometry(gt, level) {
		return out, fmt.Errorf("segeval: ground truth has no %s geometry", level)
	}
	if len(hs) == 0 && len(hyp.Elements(level)) == 0 && hasOtherGeometry(hyp, level) {
		return out, fmt.Errorf("segeval: hypothesis has no %s geometry", level)
	}
	dropped := Counts{}
	retained := func(elements []ocrpage.Element) []ocrpage.Element {
		return slices.DeleteFunc(elements, func(e ocrpage.Element) bool { return e.Box.Area() == 0 })
	}
	n := len(gs)
	gs = retained(gs)
	dropped.GroundTruth = n - len(gs)
	n = len(hs)
	hs = retained(hs)
	dropped.Hypothesis = n - len(hs)
	poly := len(gs)+len(hs) > 0
	for _, e := range append(slices.Clone(gs), hs...) {
		if len(e.Polygon) < 3 {
			poly = false
		}
	}
	out = Report{Profile: p.Name, Level: string(level), GeometryMode: "box", Primary: string(p.Primary), Calibrated: p.Calibrated, Counts: Counts{len(gs), len(hs)}}
	out.Dropped = dropped
	if poly {
		out.GeometryMode = "polygon"
	}
	step := max(1, (max(gt.Width, gt.Height)+3999)/4000)
	gm, hm := make([]mask, len(gs)), make([]mask, len(hs))
	ga, ha := make([]float64, len(gs)), make([]float64, len(hs))
	for i, e := range gs {
		gm[i] = raster(e, poly, step, gt.Height)
		ga[i] = area(gm[i], step, gt.Height)
	}
	for i, e := range hs {
		hm[i] = raster(e, poly, step, gt.Height)
		ha[i] = area(hm[i], step, gt.Height)
	}
	gu, hu := union(gm), union(hm)
	total := area(gu, step, gt.Height)
	type overlap struct {
		g, h int
		area float64
	}
	var overlaps []overlap
	var edges []pair
	idx := make([]int, len(hs))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int { return hs[a].Box.X1 - hs[b].Box.X1 })
	for i, g := range gs {
		for _, j := range idx {
			h := hs[j]
			if h.Box.X1 >= g.Box.X2 {
				break
			}
			if h.Box.X2 <= g.Box.X1 || g.Box.Intersect(h.Box).Area() == 0 {
				continue
			}
			a := intersection(gm[i], hm[j], step, gt.Height)
			if a == 0 {
				continue
			}
			overlaps = append(overlaps, overlap{i, j, a})
			iou := ratio(a, ga[i]+ha[j]-a)
			if iou >= .05 {
				edges = append(edges, pair{i, j, iou})
			}
		}
	}
	pairs := componentMatch(len(gs), len(hs), edges)
	out.ReadingOrder = orderScore(pairs)
	errs := map[string]ErrorResult{}
	names := []string{"merge_h", "merge_v", "split", "miss", "partial", "false_detection", "misclassification"}
	for _, n := range names {
		errs[n] = ErrorResult{}
	}
	add := func(name string, a float64, n int) { r := errs[name]; r.Area += a; r.Count += n; errs[name] = r }
	for i := range gs {
		covered := intersection(gm[i], hu, step, gt.Height)
		if ratio(covered, ga[i]) < p.Tolerance {
			add("miss", ga[i], 1)
		} else if ga[i]-covered > p.Tolerance*ga[i] {
			add("partial", ga[i]-covered, 1)
		}
		k, sum, largest := 0, 0.0, 0.0
		for _, e := range overlaps {
			if e.g == i && e.area >= p.Tolerance*ga[i] {
				k++
				sum += e.area
				largest = max(largest, e.area)
			}
		}
		if k >= 2 {
			add("split", sum-largest, k-1)
		}
	}
	for j := range hs {
		covered := intersection(hm[j], gu, step, gt.Height)
		if ratio(covered, ha[j]) < p.Tolerance {
			add("false_detection", ha[j], 1)
		}
		var merged []int
		sum, largest := 0.0, 0.0
		for _, e := range overlaps {
			if e.h == j && e.area >= p.Tolerance*ga[e.g] {
				merged = append(merged, e.g)
				sum += e.area
				largest = max(largest, e.area)
			}
		}
		if len(merged) >= 2 {
			horizontal := false
			for ai, a := range merged {
				for _, b := range merged[ai+1:] {
					x, y := gs[a].Box, gs[b].Box
					vertical := max(0, min(x.Y2, y.Y2)-max(x.Y1, y.Y1))
					if float64(vertical) >= .5*float64(min(x.Y2-x.Y1, y.Y2-y.Y1)) {
						horizontal = true
					}
				}
			}
			name := "merge_v"
			if horizontal {
				name = "merge_h"
			}
			add(name, sum-largest, len(merged)-1)
		}
	}
	if level == ocrpage.LevelRegion {
		for _, e := range pairs {
			if gs[e.g].Type != hs[e.h].Type {
				add("misclassification", intersection(gm[e.g], hm[e.h], step, gt.Height), 1)
			}
		}
	}
	weights := p.Weights.values()
	areaSuccess, countSuccess := []float64{}, []float64{}
	for i, name := range names {
		r := errs[name]
		denom := total
		if denom == 0 && r.Area > 0 {
			denom = r.Area
		}
		r.AreaRate = weights[i] * ratio(r.Area, denom)
		r.CountRate = weights[i] * ratio(float64(r.Count), float64(max(1, len(gs))))
		r.Success = 1 - clamp(r.AreaRate)
		errs[name] = r
		areaSuccess = append(areaSuccess, r.Success)
		countSuccess = append(countSuccess, 1-clamp(r.CountRate))
	}
	areaSuccess = append(areaSuccess, out.ReadingOrder)
	countSuccess = append(countSuccess, out.ReadingOrder)
	out.PRImA = &PRImAResult{Errors: errs, Score: weightedHarmonic(areaSuccess, weights), CountScore: weightedHarmonic(countSuccess, weights)}
	out.Score = out.PRImA.Score
	out.Matching = &MatchingResult{Matched: len(pairs)}
	prf := func(th float64) PRF {
		n := 0
		for _, e := range pairs {
			if e.iou+1e-9 >= th {
				n++
			}
		}
		precision, recall := ratio(float64(n), float64(len(hs))), ratio(float64(n), float64(len(gs)))
		if len(gs) == 0 && len(hs) == 0 {
			precision, recall = 1, 1
		}
		return PRF{precision, recall, harmonic(precision, recall)}
	}
	out.Matching.At50 = prf(.5)
	out.Matching.At75 = prf(.75)
	for i := 0; i < 10; i++ {
		out.Matching.MeanF1 += prf(.5+float64(i)*.05).F1 / 10
	}
	for _, e := range pairs {
		out.Matching.MeanIoU += e.iou
	}
	out.Matching.MeanIoU = ratio(out.Matching.MeanIoU, float64(len(pairs)))
	if len(gs) == 0 && len(hs) == 0 {
		out.Matching.MeanIoU = 1
	}
	covered := intersection(gu, hu, step, gt.Height)
	mote := &MOTeResult{Missing: 1 - ratio(covered, total)}
	if total == 0 {
		mote.Missing = 0
	}
	for _, m := range hm {
		mote.Overlap += intersection(m, gu, step, gt.Height)
	}
	mote.Overlap = ratio(mote.Overlap-covered, total)
	// Assign overlapping GT pixels to the lowest GT index, matching mote_error.
	owners := make([]mask, len(gm))
	occupied := mask{}
	for i, g := range gm {
		owners[i] = subtract(g, occupied)
		occupied = union([]mask{occupied, g})
	}
	for _, m := range hm {
		mass := 0.0
		for _, g := range owners {
			mass = max(mass, intersection(m, g, step, gt.Height))
		}
		mote.Trespass += intersection(m, gu, step, gt.Height) - mass
	}
	mote.Trespass = ratio(mote.Trespass, total)
	mote.Total = mote.Missing + mote.Overlap + mote.Trespass
	mote.Excess = clamp(ratio(area(hu, step, gt.Height)-covered, float64(gt.Width)*float64(gt.Height)-total))
	out.MOTe = mote
	hasBase := level == ocrpage.LevelLine && len(gs) > 0
	for _, g := range gs {
		if len(g.Baseline) < 2 || g.BaselineDerived {
			hasBase = false
		}
	}
	hypHasBase := slices.ContainsFunc(hs, func(h ocrpage.Element) bool { return len(h.Baseline) >= 2 })
	if hasBase && hypHasBase {
		out.CBAD = baselineScore(gs, hs)
	}
	if p.Primary == MeasureCBAD {
		if out.CBAD == nil {
			out.Primary = string(p.Fallback)
			out.FallbackReason = "ground truth has no complete real baselines"
			if hasBase && !hypHasBase {
				out.FallbackReason = "hypothesis has no baselines"
			}
		} else {
			out.Score = out.CBAD.F
		}
	}
	if level != ocrpage.LevelRegion && (p.Primary == MeasurePRImARegion || slices.Contains(p.Supporting, MeasurePRImARegion)) && gt.HasLevel(ocrpage.LevelRegion) && hyp.HasLevel(ocrpage.LevelRegion) {
		rp := p
		rp.Primary = MeasurePRImA
		rp.Supporting = nil
		r, e := Evaluate(gt, hyp, Options{Profile: rp, Level: ocrpage.LevelRegion, Ignore: o.Ignore})
		if e != nil {
			return out, e
		}
		out.PRImARegion = r.PRImA
		if p.Primary == MeasurePRImARegion {
			out.Score = r.Score
		}
	}
	if p.Primary == MeasurePRImARegion && out.PRImARegion == nil && level != ocrpage.LevelRegion {
		return out, errors.New("segeval: primary measure requires region geometry")
	}
	if slices.Contains(p.Supporting, MeasureColumns) {
		v := columnScore(gs, hs, pairs, p.Tolerance, gt)
		out.Columns = &v
	}
	return out, nil
}
func weightedHarmonic(v, w []float64) float64 {
	sum, den := 0.0, 0.0
	for i, x := range v {
		if w[i] == 0 {
			continue
		}
		if x <= 0 {
			return 0
		}
		sum += w[i]
		den += w[i] / x
	}
	if den == 0 {
		return 1
	}
	return sum / den
}
func columnScore(gs, hs []ocrpage.Element, pairs []pair, tol float64, gt ocrpage.Page) float64 {
	if len(pairs) == 0 {
		if len(gs) == 0 && len(hs) == 0 {
			return 1
		}
		return 0
	}
	type span struct{ lo, hi int }
	var cols []span
	for _, region := range gt.Regions {
		if region.Table == nil {
			continue
		}
		byColumn := map[int]span{}
		for _, cell := range region.Table.Cells {
			if cell.ColSpan != 1 {
				continue
			}
			c, ok := byColumn[cell.Col]
			if !ok {
				c = span{cell.Box.X1, cell.Box.X2}
			} else {
				c = span{min(c.lo, cell.Box.X1), max(c.hi, cell.Box.X2)}
			}
			byColumn[cell.Col] = c
		}
		for _, c := range byColumn {
			cols = append(cols, c)
		}
	}
	useCells := len(cols) > 0
	ordered := slices.Clone(gs)
	slices.SortFunc(ordered, func(a, b ocrpage.Element) int { return a.Box.X1 + a.Box.X2 - b.Box.X1 - b.Box.X2 })
	for _, g := range ordered {
		if useCells {
			break
		}
		found := false
		for i, c := range cols {
			overlap := max(0, min(c.hi, g.Box.X2)-max(c.lo, g.Box.X1))
			if float64(overlap) >= .5*float64(min(c.hi-c.lo, g.Box.X2-g.Box.X1)) {
				cols[i] = span{min(c.lo, g.Box.X1), max(c.hi, g.Box.X2)}
				found = true
				break
			}
		}
		if !found {
			cols = append(cols, span{g.Box.X1, g.Box.X2})
		}
	}
	n := 0
	for _, p := range pairs {
		g, h := gs[p.g], hs[p.h]
		for _, c := range cols {
			if g.Box.X1 >= c.lo && g.Box.X2 <= c.hi && float64(h.Box.X1) >= float64(c.lo)-tol*float64(c.hi-c.lo) && float64(h.Box.X2) <= float64(c.hi)+tol*float64(c.hi-c.lo) {
				n++
				break
			}
		}
	}
	return float64(n) / float64(len(pairs))
}
func hasOtherGeometry(p ocrpage.Page, level ocrpage.Level) bool {
	for _, l := range []ocrpage.Level{ocrpage.LevelRegion, ocrpage.LevelLine, ocrpage.LevelWord} {
		if l != level && p.HasLevel(l) {
			return true
		}
	}
	return false
}
