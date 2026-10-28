package kraken

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Recognizer applies a Kraken recognition model to an encoded crop.
// Recognition and segmentation model licenses are selected by the operator.
type Recognizer struct{ SegmentationModel string }

// Name returns the recognition provider name.
func (r *Recognizer) Name() string { return "kraken" }

// Extract runs native baseline segmentation and recognition, returning text.
func (r *Recognizer) Extract(ctx context.Context, req providers.Request) (providers.Result, error) {
	check := req
	if check.Prompt == "" {
		check.Prompt = "OCR"
	}
	if e := providers.ValidateRequest(check, 50<<20); e != nil {
		return providers.Result{}, e
	}
	if e := ctx.Err(); e != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, e)
	}
	dir, e := os.MkdirTemp("", "htr-kraken-ocr-*")
	if e != nil {
		return providers.Result{}, invalid()
	}
	defer os.RemoveAll(dir)
	input, output := dir+"/image", dir+"/text.txt"
	if e = os.WriteFile(input, req.Image.Data, 0600); e != nil {
		return providers.Result{}, invalid()
	}
	segModel := r.SegmentationModel
	if segModel == "" {
		segModel = "blla.mlmodel"
	}
	cmd := exec.CommandContext(ctx, "kraken", "--threads", "1", "-i", input, output, "segment", "-bl", "-i", segModel, "ocr", "-m", req.Model)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if e = cmd.Run(); e != nil {
		return providers.Result{}, providers.ErrorForRequest(ctx, e)
	}
	f, e := os.Open(output)
	if e != nil {
		return providers.Result{}, invalid()
	}
	b, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	ce := f.Close()
	if e != nil || ce != nil || len(b) > 16<<20 {
		return providers.Result{}, invalid()
	}
	return providers.Result{Text: strings.TrimSpace(string(b)), EffectiveModel: req.Model}, nil
}

// Provider adapts the Kraken recognizer for CLI evaluation and pipelines.
type Provider struct{}

// Name returns the registry name.
func (p *Provider) Name() string { return "kraken" }

// ValidateConfig requires an explicitly selected recognition model.
func (p *Provider) ValidateConfig(c providers.Config) error {
	if strings.TrimSpace(c.Model) == "" {
		return providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	return nil
}

// ExtractText adapts historical base64 input to the recognition client.
func (p *Provider) ExtractText(ctx context.Context, c providers.Config, path, encoded string) (string, providers.UsageInfo, error) {
	req, e := providers.LegacyRequest(c, path, encoded, 50<<20)
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	r := &Recognizer{SegmentationModel: os.Getenv("KRAKEN_SEGMENTATION_MODEL")}
	result, e := r.Extract(ctx, req)
	return result.Text, result.Usage, e
}
