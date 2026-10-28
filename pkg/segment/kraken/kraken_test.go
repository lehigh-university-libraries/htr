package kraken

import (
	"bytes"
	"context"
	"errors"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubprocessAndRedaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kraken")
	if e := os.WriteFile(path, []byte("#!/bin/sh\ncat > \"$3\" <<'JSON'\n{\"lines\":[{\"boundary\":[[0,0],[80,0],[80,20],[0,20]],\"baseline\":[[0,15],[80,15]],\"tags\":{\"type\":\"default\"}}]}\nJSON\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var b bytes.Buffer
	if e := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 100, 100))); e != nil {
		t.Fatal(e)
	}
	req := segment.Request{Image: providers.Image{Data: b.Bytes(), MediaType: "image/png"}}
	r, e := New("").Segment(context.Background(), req)
	if e != nil || len(r.Page.Lines()) != 1 || len(r.Page.Lines()[0].Baseline) != 2 {
		t.Fatal(r, e)
	}
	if e = os.WriteFile(path, []byte("#!/bin/sh\necho 'failed to load private-model-path: invalid model' >&2\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	_, e = New("private-model-path").Segment(context.Background(), req)
	if e == nil || strings.Contains(e.Error(), "private") || !strings.Contains(e.Error(), "invalid model") {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = New("").Segment(ctx, req)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	_, e = New("").SegmentFile(context.Background(), "-input.png", req)
	var providerErr *providers.Error
	if !errors.As(e, &providerErr) || providerErr.Kind != providers.ErrorInvalidRequest {
		t.Fatalf("flag-like path accepted: %v", e)
	}
}

func TestDiagnosticTailIsBounded(t *testing.T) {
	var tail stderrTail
	_, _ = tail.Write([]byte(strings.Repeat("x", 5000)))
	_, _ = tail.Write([]byte("last line"))
	if len(tail.data) != 4096 || !strings.HasSuffix(string(tail.data), "last line") {
		t.Fatal("missing bounded diagnostic tail")
	}
}
