package pipeline

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"sync/atomic"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

type stubSegmenter struct{ p ocrpage.Page }

func (s stubSegmenter) Name() string { return "stub" }
func (s stubSegmenter) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelLine}}
}
func (s stubSegmenter) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	return segment.Result{Page: s.p}, nil
}

type recognizerFunc func(context.Context, providers.Request) (providers.Result, error)

func (f recognizerFunc) Name() string { return "stub" }
func (f recognizerFunc) Extract(c context.Context, r providers.Request) (providers.Result, error) {
	return f(c, r)
}
func TestRun(t *testing.T) {
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 100, 100))); e != nil {
		t.Fatal(e)
	}
	p := ocrpage.FromLines(100, 100, []ocrpage.Line{{Box: ocrpage.Rect{X1: 10, Y1: 10, X2: 60, Y2: 30}}, {Box: ocrpage.Rect{X1: 10, Y1: 40, X2: 60, Y2: 60}}, {Box: ocrpage.Rect{X1: 10, Y1: 70, X2: 60, Y2: 90}}})
	var active, maximum, calls atomic.Int32
	rec := recognizerFunc(func(ctx context.Context, r providers.Request) (providers.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := maximum.Load()
			if n <= m || maximum.CompareAndSwap(m, n) {
				break
			}
		}
		calls.Add(1)
		cfg, _, e := image.DecodeConfig(bytes.NewReader(r.Image.Data))
		if e != nil || cfg.Width != 54 || cfg.Height != 24 {
			t.Errorf("crop %+v %v", cfg, e)
		}
		if r.Model != "recognizer" || r.Prompt != "read" || r.Image.MediaType != "image/png" {
			t.Error("lost recognition configuration")
		}
		return providers.Result{Text: "été 水"}, nil
	})
	got, e := Run(context.Background(), stubSegmenter{p}, rec, providers.Image{Data: encoded.Bytes(), MediaType: "image/png"}, Options{Concurrency: 2, PadPixels: 2, Model: "recognizer", Prompt: "read"})
	if e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 3 || maximum.Load() > 2 || got.PlainText() != "été 水\nété 水\nété 水" {
		t.Fatal(got, calls.Load(), maximum.Load())
	}
	if p.PlainText() != "\n\n" {
		t.Fatal("mutated segmenter output")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Run(ctx, stubSegmenter{p}, rec, providers.Image{Data: encoded.Bytes()}, Options{}); !errors.Is(e, context.Canceled) {
		t.Fatalf("lost cancellation: %v", e)
	}
	if calls.Load() != 3 {
		t.Fatal("recognized after cancellation")
	}
}
