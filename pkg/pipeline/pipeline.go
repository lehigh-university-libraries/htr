// Package pipeline composes a segmenter and byte-oriented transcription client.
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
	"golang.org/x/sync/errgroup"
)

// Options controls crop geometry, recognition and bounded concurrency.
type Options struct {
	Level         ocrpage.Level
	Concurrency   int
	Prompt, Model string
	PadPixels     int
	SegmentModel  string
	Hints         segment.Hints
}

// Run segments an image, recognizes bounded crops and fills the corresponding text.
func Run(ctx context.Context, seg segment.Segmenter, rec providers.Client, img providers.Image, o Options) (ocrpage.Page, error) {
	if o.Level == "" {
		o.Level = ocrpage.LevelLine
	}
	if o.Concurrency == 0 {
		o.Concurrency = 1
	}
	if o.Concurrency < 1 || o.Concurrency > 256 || o.PadPixels < 0 {
		return ocrpage.Page{}, errors.New("pipeline: invalid options")
	}
	if err := segment.RequireLevel(seg, o.SegmentModel, o.Level); err != nil {
		return ocrpage.Page{}, err
	}
	r, err := seg.Segment(ctx, segment.Request{Image: img, Model: o.SegmentModel, Hints: o.Hints})
	if err != nil {
		return ocrpage.Page{}, err
	}
	if err = ocrpage.Validate(r.Page, ocrpage.Limits{}); err != nil {
		return ocrpage.Page{}, err
	}
	if err = ctx.Err(); err != nil {
		return ocrpage.Page{}, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
		return ocrpage.Page{}, errors.New("pipeline: invalid or oversized image")
	}
	if r.Page.Width != cfg.Width || r.Page.Height != cfg.Height {
		return ocrpage.Page{}, errors.New("pipeline: segmentation dimensions differ from image")
	}
	decoded, _, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return ocrpage.Page{}, errors.New("pipeline: invalid image")
	}
	sub, ok := decoded.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return ocrpage.Page{}, errors.New("pipeline: image cannot be cropped")
	}
	p := r.Page.Scale(r.Page.Width, r.Page.Height)
	type target struct {
		box ocrpage.Rect
		set func(string)
	}
	var targets []target
	for i := range p.Regions {
		r := &p.Regions[i]
		if o.Level == ocrpage.LevelRegion {
			targets = append(targets, target{r.Box, func(s string) { r.Lines = []ocrpage.Line{{Box: r.Box, Text: s}} }})
			continue
		}
		for j := range r.Lines {
			l := &r.Lines[j]
			if o.Level == ocrpage.LevelLine {
				targets = append(targets, target{l.Box, func(s string) { l.Text = s; l.Words = nil }})
				continue
			}
			for k := range l.Words {
				w := &l.Words[k]
				targets = append(targets, target{w.Box, func(s string) { w.Text = s }})
			}
		}
	}
	g, workerCtx := errgroup.WithContext(ctx)
	g.SetLimit(o.Concurrency)
	for _, t := range targets {
		if workerCtx.Err() != nil {
			break
		}
		g.Go(func() error {
			if e := workerCtx.Err(); e != nil {
				return e
			}
			b := t.box
			crop := image.Rect(max(0, b.X1-o.PadPixels), max(0, b.Y1-o.PadPixels), min(p.Width, b.X2+o.PadPixels), min(p.Height, b.Y2+o.PadPixels))
			if crop.Empty() {
				return errors.New("pipeline: empty crop")
			}
			var data bytes.Buffer
			if e := png.Encode(&data, sub.SubImage(crop)); e != nil {
				return e
			}
			result, e := rec.Extract(workerCtx, providers.Request{Model: o.Model, Prompt: o.Prompt, Image: providers.Image{Data: data.Bytes(), MediaType: "image/png"}})
			if e != nil {
				return e
			}
			t.set(result.Text)
			return nil
		})
	}
	if err = g.Wait(); err != nil {
		return ocrpage.Page{}, err
	}
	if err = ctx.Err(); err != nil {
		return ocrpage.Page{}, err
	}
	return p, nil
}
