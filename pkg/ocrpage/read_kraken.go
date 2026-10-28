package ocrpage

import (
	"encoding/json"
	"errors"
	"slices"
)

func readKraken(data []byte, o ReadOptions) (Page, error) {
	if err := checkJSON(data, o.Limits.defaults()); err != nil {
		return Page{}, err
	}
	type nativeLine struct {
		ID                 string `json:"id"`
		Baseline, Boundary [][]int
		Text               string `json:"text"`
		Region             string `json:"region"`
	}
	var v struct {
		Width, Height int
		Lines         []nativeLine
		Regions       map[string][]struct {
			ID       string  `json:"id"`
			Boundary [][]int `json:"boundary"`
		}
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return Page{}, errors.New("ocrpage: invalid Kraken JSON")
	}
	convert := func(v [][]int) ([]Point, error) {
		var p []Point
		for _, a := range v {
			if len(a) != 2 {
				return nil, errors.New("ocrpage: invalid Kraken point")
			}
			p = append(p, Point{a[0], a[1]})
		}
		return p, nil
	}
	p := Page{Width: v.Width, Height: v.Height}
	if p.Width == 0 {
		p.Width = o.CanvasWidth
	}
	if p.Height == 0 {
		p.Height = o.CanvasHeight
	}
	index := map[string]int{}
	labels := make([]string, 0, len(v.Regions))
	for label := range v.Regions {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	for _, label := range labels {
		regions := v.Regions[label]
		for _, n := range regions {
			poly, e := convert(n.Boundary)
			if e != nil {
				return Page{}, e
			}
			index[n.ID] = len(p.Regions)
			p.Regions = append(p.Regions, Region{ID: n.ID, Type: CanonicalType(label), SourceType: label, Box: BoundingBox(poly), Polygon: poly})
		}
	}
	implicit := -1
	for _, n := range v.Lines {
		poly, e := convert(n.Boundary)
		if e != nil {
			return Page{}, e
		}
		base, e := convert(n.Baseline)
		if e != nil {
			return Page{}, e
		}
		b := BoundingBox(poly)
		if b.Area() == 0 {
			b = BoundingBox(base)
			if len(base) < 2 || b.X2 <= b.X1 {
				return Page{}, errors.New("ocrpage: Kraken line has no geometry")
			}
			height := max(20, b.Y2-b.Y1)
			b.Y1 = max(0, b.Y1-height/2)
			b.Y2 = b.Y1 + height
			if p.Height > 0 {
				b.Y2 = min(p.Height, b.Y2)
			}
		}
		l := Line{ID: n.ID, Box: b, Polygon: poly, Baseline: base, Text: n.Text}
		idx, ok := index[n.Region]
		if !ok {
			if implicit < 0 {
				implicit = len(p.Regions)
				p.Regions = append(p.Regions, Region{Type: RegionText, Implicit: true})
			}
			idx = implicit
		}
		r := &p.Regions[idx]
		r.Lines = append(r.Lines, l)
		if r.Implicit {
			r.Box = r.Box.Union(b)
		}
	}
	return p, nil
}
