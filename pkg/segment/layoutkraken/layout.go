// Package layoutkraken combines ordered layout regions and native Kraken lines.
package layoutkraken

import (
	"context"
	"errors"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// Segmenter composes independently configured layout and line providers.
type Segmenter struct{ Layout, Lines segment.Segmenter }

// Name returns the registry name.
func (s *Segmenter) Name() string { return "layout+kraken" }

// Capabilities declares region and line geometry with native baselines.
func (s *Segmenter) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelRegion, ocrpage.LevelLine}, Polygons: true, Baselines: true, ReadingOrder: true, RegionTypes: true}
}

// Combine assigns each native line to the greatest-overlap region exactly once.
func Combine(layout, lines ocrpage.Page) (ocrpage.Page, error) {
	if layout.Width != lines.Width || layout.Height != lines.Height {
		return ocrpage.Page{}, errors.New("layout+kraken: page dimensions differ")
	}
	p := layout.Scale(layout.Width, layout.Height)
	for i := range p.Regions {
		p.Regions[i].Lines = nil
	}
	var unassigned []ocrpage.Line
	for _, l := range lines.Lines() {
		best := -1
		area := 0.0
		for i, r := range p.Regions {
			overlap := l.Box.Intersect(r.Box).Area()
			if overlap > area {
				best, area = i, overlap
			}
		}
		if best < 0 {
			unassigned = append(unassigned, l)
		} else {
			p.Regions[best].Lines = append(p.Regions[best].Lines, l)
		}
	}
	if len(unassigned) > 0 {
		implicit := ocrpage.FromLines(p.Width, p.Height, unassigned)
		p.Regions = append(p.Regions, implicit.Regions...)
	}
	return p, nil
}

// Segment runs both segmenters and combines their page geometry.
func (s *Segmenter) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	layoutReq := r
	layoutReq.Model = ""
	layout, e := s.Layout.Segment(ctx, layoutReq)
	if e != nil {
		return segment.Result{}, e
	}
	lines, e := s.Lines.Segment(ctx, r)
	if e != nil {
		return segment.Result{}, e
	}
	return combineResult(layout, lines)
}

// SegmentFile reuses the prepared image for local components.
func (s *Segmenter) SegmentFile(ctx context.Context, path string, r segment.Request) (segment.Result, error) {
	layout, ok := s.Layout.(segment.FileSegmenter)
	if !ok {
		return segment.Result{}, errors.New("layout+kraken: layout requires byte input")
	}
	lines, ok := s.Lines.(segment.FileSegmenter)
	if !ok {
		return segment.Result{}, errors.New("layout+kraken: lines require byte input")
	}
	lr := r
	lr.Model = ""
	a, e := layout.SegmentFile(ctx, path, lr)
	if e != nil {
		return segment.Result{}, e
	}
	b, e := lines.SegmentFile(ctx, path, r)
	if e != nil {
		return segment.Result{}, e
	}
	return combineResult(a, b)
}
func combineResult(a, b segment.Result) (segment.Result, error) {
	p, e := Combine(a.Page, b.Page)
	if e != nil {
		return segment.Result{}, e
	}
	raw, e := ocrpage.WriteJSON(p)
	if e != nil {
		return segment.Result{}, e
	}
	return segment.Result{Page: p, Raw: raw, RawFormat: "htrjson", EffectiveModel: a.EffectiveModel + "+" + b.EffectiveModel}, nil
}
