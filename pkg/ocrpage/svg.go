package ocrpage

import (
	"errors"
	"regexp"
)

var svgPointAttribute = regexp.MustCompile(`(<(?:polygon|polyline)\b[^>]*\s+points\s*=\s*["'])([^"']*)(["'])`)

// MapSVGPoints changes polygon and polyline coordinates while preserving the
// surrounding SVG and selector properties. Unsupported SVG shapes are left
// untouched; no baseline or polygon is inferred from them.
func MapSVGPoints(svg string, transform func(Point) Point) (string, error) {
	if transform == nil {
		return "", errors.New("ocrpage: missing SVG transform")
	}
	if _, err := readXML([]byte(svg), false, Limits{MaxBytes: 1 << 20, MaxDepth: 16, MaxElements: 10000}.defaults()); err != nil {
		return "", err
	}
	var failure error
	result := svgPointAttribute.ReplaceAllStringFunc(svg, func(value string) string {
		parts := svgPointAttribute.FindStringSubmatch(value)
		ps, err := points(parts[2])
		if err != nil {
			failure = err
			return value
		}
		for i, p := range ps {
			ps[i] = transform(p)
		}
		return parts[1] + pointString(ps) + parts[3]
	})
	return result, failure
}
