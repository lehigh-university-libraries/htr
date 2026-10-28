// Package ppdoclayout runs the Apache-2.0 PP-DocLayoutV3 detector via PaddleOCR.
package ppdoclayout

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// Options configures administrator-owned local model and device selection.
type Options struct{ ModelDir, Device string }

// Segmenter invokes the installed PaddleOCR executable without a shell.
type Segmenter struct{ options Options }

// New creates a local detector; the device defaults to cpu.
func New(o Options) *Segmenter {
	if o.Device == "" {
		o.Device = "cpu"
	}
	return &Segmenter{o}
}

// Name returns the registry name.
func (s *Segmenter) Name() string { return "ppdoclayout" }

// Capabilities declares typed region geometry and reading order.
func (s *Segmenter) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelRegion}, Polygons: true, RegionTypes: true, ReadingOrder: true, Confidence: true}
}
func failure() error { return providers.NewError(providers.ErrorInvalidResponse, 0, false, nil) }

// RegionType maps detector labels; unknown labels are retained in SourceType.
func RegionType(s string) ocrpage.RegionType {
	switch s {
	case "doc_title", "paragraph_title":
		return ocrpage.RegionHeading
	case "figure_title", "table_title", "chart_title":
		return ocrpage.RegionCaption
	case "header", "header_image":
		return ocrpage.RegionHeader
	case "footer", "footer_image":
		return ocrpage.RegionFooter
	case "number":
		return ocrpage.RegionPageNumber
	case "footnote", "vision_footnote":
		return ocrpage.RegionFootnote
	case "aside_text":
		return ocrpage.RegionMarginalia
	case "image", "chart":
		return ocrpage.RegionImage
	case "formula", "display_formula", "inline_formula", "formula_number":
		return ocrpage.RegionMath
	case "seal":
		return ocrpage.RegionSignature
	case "abstract", "algorithm", "reference", "reference_content", "content":
		return ocrpage.RegionText
	}
	return ocrpage.CanonicalType(s)
}

// ParseLayout reads saved PaddleX boxes, including polygon_points when available.
func ParseLayout(raw []byte, size image.Point) (ocrpage.Page, error) {
	if len(raw) > 16<<20 {
		return ocrpage.Page{}, failure()
	}
	type box struct {
		Label      string
		Coordinate []float64
		Polygon    [][]float64 `json:"polygon_points"`
		Order      *int
		Score      float64
	}
	var v struct{ Boxes []box }
	if e := json.Unmarshal(raw, &v); e != nil || v.Boxes == nil || len(v.Boxes) > 4096 {
		return ocrpage.Page{}, failure()
	}
	slices.SortStableFunc(v.Boxes, func(a, b box) int {
		if a.Order == nil && b.Order == nil {
			return 0
		}
		if a.Order == nil {
			return 1
		}
		if b.Order == nil {
			return -1
		}
		if *a.Order < *b.Order {
			return -1
		}
		if *a.Order > *b.Order {
			return 1
		}
		return 0
	})
	p := ocrpage.Page{Width: size.X, Height: size.Y}
	coordinate := func(v float64) (int, bool) {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1_000_000 {
			return 0, false
		}
		return int(math.Round(v)), true
	}
	for i, b := range v.Boxes {
		if len(b.Coordinate) != 4 || (b.Order != nil && *b.Order < 0) {
			return ocrpage.Page{}, failure()
		}
		var c [4]int
		for i, v := range b.Coordinate {
			n, ok := coordinate(v)
			if !ok {
				return ocrpage.Page{}, failure()
			}
			c[i] = n
		}
		r := ocrpage.Region{ID: "region_" + strconv.Itoa(i+1), Type: RegionType(b.Label), SourceType: b.Label, Box: ocrpage.Rect{X1: c[0], Y1: c[1], X2: c[2], Y2: c[3]}, Confidence: b.Score}
		for _, pt := range b.Polygon {
			if len(pt) != 2 {
				return ocrpage.Page{}, failure()
			}
			x, ok := coordinate(pt[0])
			if !ok {
				return ocrpage.Page{}, failure()
			}
			y, ok := coordinate(pt[1])
			if !ok {
				return ocrpage.Page{}, failure()
			}
			r.Polygon = append(r.Polygon, ocrpage.Point{X: x, Y: y})
		}
		if r.Box.Area() == 0 {
			return ocrpage.Page{}, failure()
		}
		p.Regions = append(p.Regions, r)
	}
	if e := ocrpage.Validate(p, ocrpage.Limits{}); e != nil {
		return ocrpage.Page{}, failure()
	}
	return p, nil
}

// Segment writes a private image file before local inference.
func (s *Segmenter) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	if e := providers.ValidateImage(r.Image, 50<<20); e != nil {
		return segment.Result{}, e
	}
	f, e := os.CreateTemp("", "htr-layout-image-*")
	if e != nil {
		return segment.Result{}, failure()
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(r.Image.Data); e != nil {
		_ = f.Close() // The failed write already makes this file unusable.
		return segment.Result{}, failure()
	}
	if e = f.Close(); e != nil {
		return segment.Result{}, failure()
	}
	return s.SegmentFile(ctx, f.Name(), r)
}

// SegmentFile detects regions from a prepared local image.
func (s *Segmenter) SegmentFile(ctx context.Context, path string, r segment.Request) (segment.Result, error) {
	if r.Model != "" && r.Model != "PP-DocLayoutV3" {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	f, e := os.Open(path)
	if e != nil {
		return segment.Result{}, failure()
	}
	cfg, _, e := image.DecodeConfig(io.LimitReader(f, 50<<20))
	ce := f.Close()
	if e != nil || ce != nil {
		return segment.Result{}, failure()
	}
	dir, e := os.MkdirTemp("", "htr-layout-*")
	if e != nil {
		return segment.Result{}, failure()
	}
	defer os.RemoveAll(dir)
	args := []string{"layout_detection", "-i", path, "--model_name", "PP-DocLayoutV3", "--device", s.options.Device, "--save_path", dir}
	if s.options.ModelDir != "" {
		args = append(args, "--model_dir", s.options.ModelDir)
	}
	cmd := exec.CommandContext(ctx, "paddleocr", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if e = cmd.Run(); e != nil {
		return segment.Result{}, providers.ErrorForRequest(ctx, e)
	}
	files, e := filepath.Glob(filepath.Join(dir, "*.json"))
	if e != nil || len(files) != 1 {
		return segment.Result{}, failure()
	}
	f, e = os.Open(files[0])
	if e != nil {
		return segment.Result{}, failure()
	}
	raw, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	ce = f.Close()
	if e != nil || ce != nil || len(raw) > 16<<20 {
		return segment.Result{}, failure()
	}
	p, e := ParseLayout(raw, image.Pt(cfg.Width, cfg.Height))
	if e != nil {
		return segment.Result{}, e
	}
	return segment.Result{Page: p, EffectiveModel: "PP-DocLayoutV3", Raw: bytes.Clone(raw), RawFormat: "ppdoclayout-json"}, nil
}
