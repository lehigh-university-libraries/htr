package ocrpage

import "math"

// SplitBaselineX cuts a baseline at an x coordinate, interpolating crossings.
// The original direction is retained; neither side gains guessed points.
func SplitBaselineX(points []Point, x int) (left, right []Point) {
	appendPoint := func(dst []Point, p Point) []Point {
		if len(dst) == 0 || dst[len(dst)-1] != p {
			return append(dst, p)
		}
		return dst
	}
	for i, p := range points {
		if i > 0 {
			prev := points[i-1]
			if (prev.X < x && p.X > x) || (prev.X > x && p.X < x) {
				cross := Point{X: x, Y: prev.Y + int(math.Round(float64(p.Y-prev.Y)*float64(x-prev.X)/float64(p.X-prev.X)))}
				left = appendPoint(left, cross)
				right = appendPoint(right, cross)
			}
		}
		if p.X <= x {
			left = appendPoint(left, p)
		}
		if p.X >= x {
			right = appendPoint(right, p)
		}
	}
	if len(left) < 2 {
		left = nil
	}
	if len(right) < 2 {
		right = nil
	}
	return
}
