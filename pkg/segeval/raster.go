package segeval

import (
	"math"
	"slices"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
)

type interval struct{ lo, hi float64 }
type mask map[int][]interval

func merge(v []interval) []interval {
	slices.SortFunc(v, func(a, b interval) int {
		if a.lo < b.lo {
			return -1
		}
		if a.lo > b.lo {
			return 1
		}
		return 0
	})
	var out []interval
	for _, r := range v {
		if r.hi <= r.lo {
			continue
		}
		if len(out) > 0 && r.lo <= out[len(out)-1].hi {
			out[len(out)-1].hi = max(out[len(out)-1].hi, r.hi)
		} else {
			out = append(out, r)
		}
	}
	return out
}
func raster(e ocrpage.Element, poly bool, step, height int) mask {
	m := mask{}
	ps := e.Box.Polygon()
	if poly {
		ps = e.Polygon
	}
	if len(ps) < 3 {
		return m
	}
	b := ocrpage.BoundingBox(ps)
	start := (b.Y1 / step) * step
	for y := start; y < min(b.Y2, height); y += step {
		cy := float64(y) + float64(min(step, height-y))/2
		var xs []float64
		for i, a := range ps {
			b := ps[(i+1)%len(ps)]
			if (float64(a.Y) <= cy && float64(b.Y) > cy) || (float64(b.Y) <= cy && float64(a.Y) > cy) {
				xs = append(xs, float64(a.X)+(cy-float64(a.Y))*float64(b.X-a.X)/float64(b.Y-a.Y))
			}
		}
		slices.Sort(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			if xs[i+1] > xs[i] {
				m[y] = append(m[y], interval{xs[i], xs[i+1]})
			}
		}
	}
	return m
}
func area(m mask, step, height int) float64 {
	v := 0.0
	for y, rows := range m {
		for _, r := range rows {
			v += (r.hi - r.lo) * float64(min(step, height-y))
		}
	}
	return v
}
func intersection(a, b mask, step, height int) float64 {
	v := 0.0
	for y, ar := range a {
		br := b[y]
		i, j := 0, 0
		for i < len(ar) && j < len(br) {
			lo, hi := max(ar[i].lo, br[j].lo), min(ar[i].hi, br[j].hi)
			if hi > lo {
				v += (hi - lo) * float64(min(step, height-y))
			}
			if ar[i].hi < br[j].hi {
				i++
			} else {
				j++
			}
		}
	}
	return v
}
func union(ms []mask) mask {
	m := mask{}
	for _, a := range ms {
		for y, r := range a {
			m[y] = append(m[y], r...)
		}
	}
	for y, r := range m {
		m[y] = merge(r)
	}
	return m
}
func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
func harmonic(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}
func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }
func subtract(a, b mask) mask {
	out := mask{}
	for y, rows := range a {
		for _, r := range rows {
			lo := r.lo
			for _, cut := range b[y] {
				if cut.hi <= lo {
					continue
				}
				if cut.lo >= r.hi {
					break
				}
				if cut.lo > lo {
					out[y] = append(out[y], interval{lo, min(cut.lo, r.hi)})
				}
				lo = max(lo, cut.hi)
				if lo >= r.hi {
					break
				}
			}
			if lo < r.hi {
				out[y] = append(out[y], interval{lo, r.hi})
			}
		}
	}
	return out
}
