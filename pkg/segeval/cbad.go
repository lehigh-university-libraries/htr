package segeval

import (
	"math"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
)

// Baseline scoring follows READ-BAD's adaptive-tolerance definition: L1 point
// distance, union coverage for recall, and greedy one-to-one precision. This is
// an independent implementation of the algorithm, not translated Java source.
// ponytail: point comparisons are quadratic; add a spatial index if dense-page
// evaluation time becomes material (the 100-line synthetic page takes ~5s).
func baselineScore(gs, hs []ocrpage.Element) *CBADResult {
	g, h := make([][]ocrpage.Point, len(gs)), make([][]ocrpage.Point, len(hs))
	for i, e := range gs {
		g[i] = sampleBaseline(e.Baseline)
	}
	for i, e := range hs {
		h[i] = sampleBaseline(e.Baseline)
	}
	tols := baselineTolerances(g)
	r := &CBADResult{}
	for i, line := range g {
		sum := 0.0
		for _, p := range line {
			d := math.Inf(1)
			for _, other := range h {
				d = min(d, nearest(p, other))
			}
			sum += softCoverage(d, tols[i])
		}
		r.Recall += ratio(sum, float64(len(line)))
	}
	r.Recall = ratio(r.Recall, float64(len(g)))
	weights := make([][]float64, len(h))
	for j, line := range h {
		weights[j] = make([]float64, len(g))
		for i, other := range g {
			sum := 0.0
			for _, p := range line {
				sum += softCoverage(nearest(p, other), tols[i])
			}
			weights[j][i] = ratio(sum, float64(len(line)))
		}
	}
	usedG, usedH := make([]bool, len(g)), make([]bool, len(h))
	for {
		best := 0.0
		bi, bj := -1, -1
		for j, row := range weights {
			if usedH[j] {
				continue
			}
			for i, v := range row {
				if !usedG[i] && v > best {
					best = v
					bi, bj = i, j
				}
			}
		}
		if bi < 0 {
			break
		}
		usedG[bi], usedH[bj] = true, true
		r.Precision += best
	}
	r.Precision = ratio(r.Precision, float64(len(h)))
	r.F = harmonic(r.Precision, r.Recall)
	distance := func(a, b [][]ocrpage.Point) float64 {
		sum, n := 0.0, 0
		for _, line := range a {
			for _, p := range line {
				d := math.Inf(1)
				for _, other := range b {
					d = min(d, nearest(p, other))
				}
				if math.IsInf(d, 1) {
					continue
				}
				sum += d
				n++
			}
		}
		return ratio(sum, float64(n))
	}
	r.Chamfer = (distance(g, h) + distance(h, g)) / 2
	for _, line := range h {
		if len(line) > 0 {
			r.ChamferDefined = true
			break
		}
	}
	return r
}
func nearest(p ocrpage.Point, line []ocrpage.Point) float64 {
	d := math.Inf(1)
	for _, q := range line {
		d = min(d, math.Abs(float64(p.X-q.X))+math.Abs(float64(p.Y-q.Y)))
	}
	return d
}
func softCoverage(d, t float64) float64 {
	if d <= t {
		return 1
	}
	if t == 0 {
		return 0
	}
	return max(0, (3*t-d)/(2*t))
}
func sampleBaseline(line []ocrpage.Point) []ocrpage.Point {
	if len(line) < 2 {
		return nil
	}
	var dense []ocrpage.Point
	for i := 1; i < len(line); i++ {
		a, b := line[i-1], line[i]
		n := max(abs(a.X-b.X), abs(a.Y-b.Y))
		for k := 0; k < n; k++ {
			f := float64(k) / float64(n)
			dense = append(dense, ocrpage.Point{X: int(math.Floor(float64(a.X) + f*float64(b.X-a.X) + .5)), Y: int(math.Floor(float64(a.Y) + f*float64(b.Y-a.Y) + .5))})
		}
	}
	dense = append(dense, line[len(line)-1])
	if len(dense) <= 20 {
		return dense
	}
	count := max(20, (len(dense)-1)/5+1)
	out := make([]ocrpage.Point, count)
	for i := range out {
		idx := int(float64(i) * float64(len(dense)-1) / float64(count-1))
		out[i] = dense[idx]
	}
	return out
}
func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
func baselineTolerances(lines [][]ocrpage.Point) []float64 {
	t := make([]float64, len(lines))
	sum, n := 0.0, 0
	for i, line := range lines {
		if len(line) < 2 {
			continue
		}
		mx, my := 0.0, 0.0
		for _, p := range line {
			mx += float64(p.X)
			my += float64(p.Y)
		}
		mx /= float64(len(line))
		my /= float64(len(line))
		xx, xy := 0.0, 0.0
		for _, p := range line {
			x, y := float64(p.X)-mx, float64(p.Y)-my
			xx += x * x
			xy += x * y
		}
		ux, uy := 0.0, 1.0
		if xx > 0 {
			slope := xy / xx
			ux = 1 / math.Sqrt(1+slope*slope)
			uy = slope * ux
		}
		projection := func(p ocrpage.Point) float64 { return float64(p.X)*ux + float64(p.Y)*uy }
		alo, ahi := projection(line[0]), projection(line[len(line)-1])
		if alo > ahi {
			alo, ahi = ahi, alo
		}
		best := 250.0
		for j, other := range lines {
			if i == j || len(other) < 2 {
				continue
			}
			blo, bhi := projection(other[0]), projection(other[len(other)-1])
			if blo > bhi {
				blo, bhi = bhi, blo
			}
			if bhi < alo || blo > ahi {
				continue
			}
			for _, p := range line {
				for _, q := range other {
					dx, dy := float64(p.X-q.X), float64(p.Y-q.Y)
					if math.Abs(dx*ux+dy*uy) <= 10 {
						best = min(best, math.Abs(dx*uy-dy*ux))
					}
				}
			}
		}
		if best < 250 {
			t[i] = best
			if best != 0 {
				sum += best
				n++
			}
		}
	}
	mean := 250.0
	if n > 0 {
		mean = sum / float64(n)
	}
	for i, v := range t {
		if v == 0 {
			v = mean
		}
		t[i] = min(v, mean) * .25
	}
	return t
}
