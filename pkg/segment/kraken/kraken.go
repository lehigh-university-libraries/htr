// Package kraken runs the Apache-2.0 Kraken line segmenter. Model licenses vary.
package kraken

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// Segmenter invokes Kraken directly, without a shell.
type Segmenter struct{ Model string }

// New creates a segmenter, defaulting to blla.mlmodel.
func New(model string) *Segmenter {
	if model == "" {
		model = "blla.mlmodel"
	}
	return &Segmenter{Model: model}
}

// Name returns the registry name.
func (s *Segmenter) Name() string { return "kraken" }

// Capabilities declares line polygons and baselines. Native regions are preserved when present.
func (s *Segmenter) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelLine}, Polygons: true, Baselines: true, ReadingOrder: true}
}
func invalid() error { return providers.NewError(providers.ErrorInvalidResponse, 0, false, nil) }

// ParseKraken parses native segmentation using the supplied image dimensions.
func ParseKraken(raw []byte, size image.Point) (ocrpage.Page, error) {
	return ocrpage.ParseFormat(raw, ocrpage.FormatKraken, ocrpage.ReadOptions{CanvasWidth: size.X, CanvasHeight: size.Y})
}

// Segment writes a private temporary image before invoking Kraken.
func (s *Segmenter) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	if len(r.Image.Data) == 0 || len(r.Image.Data) > 50<<20 {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	tmp, e := os.CreateTemp("", "htr-kraken-image-*")
	if e != nil {
		return segment.Result{}, invalid()
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(r.Image.Data); e != nil {
		_ = tmp.Close() // The failed write already makes this file unusable.
		return segment.Result{}, invalid()
	}
	if e = tmp.Close(); e != nil {
		return segment.Result{}, invalid()
	}
	return s.SegmentFile(ctx, tmp.Name(), r)
}

// SegmentFile segments an existing image and bounds the native output.
func (s *Segmenter) SegmentFile(ctx context.Context, path string, r segment.Request) (segment.Result, error) {
	if strings.HasPrefix(path, "-") {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	file, e := os.Open(path)
	if e != nil {
		return segment.Result{}, invalid()
	}
	cfg, _, e := image.DecodeConfig(io.LimitReader(file, 50<<20))
	ce := file.Close()
	if e != nil || ce != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	tmp, e := os.CreateTemp("", "htr-kraken-*.json")
	if e != nil {
		return segment.Result{}, invalid()
	}
	defer os.Remove(tmp.Name())
	if e = tmp.Close(); e != nil {
		return segment.Result{}, invalid()
	}
	model := r.Model
	if model == "" {
		model = s.Model
	}
	cmd := exec.CommandContext(ctx, "kraken", "-i", path, tmp.Name(), "segment", "-bl", "-i", model)
	var stderr stderrTail
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if e = cmd.Run(); e != nil {
		return segment.Result{}, stderr.failure(ctx, e, path, tmp.Name(), model)
	}
	file, e = os.Open(tmp.Name())
	if e != nil {
		return segment.Result{}, invalid()
	}
	raw, e := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	ce = file.Close()
	if e != nil || ce != nil || len(raw) > 64<<20 {
		return segment.Result{}, invalid()
	}
	p, e := ParseKraken(raw, image.Pt(cfg.Width, cfg.Height))
	if e != nil {
		return segment.Result{}, invalid()
	}
	return segment.Result{Page: p, Raw: bytes.Clone(raw), RawFormat: "kraken", EffectiveModel: model}, nil
}
