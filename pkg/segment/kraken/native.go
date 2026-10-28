package kraken

import (
	"context"
	"encoding/json"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// NativeOptions selects operator-owned models and a Kraken JSON output template.
type NativeOptions struct{ SegmentationModel, RecognitionModel, TemplatePath string }

// RecognizeFile preserves native polygons, baselines, character cuts and word confidence.
func RecognizeFile(ctx context.Context, path string, o NativeOptions) (segment.Result, error) {
	if strings.HasPrefix(path, "-") || o.RecognitionModel == "" || o.SegmentationModel == "" || o.TemplatePath == "" {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	input, err := os.Open(path)
	if err != nil {
		return segment.Result{}, invalid()
	}
	cfg, _, err := image.DecodeConfig(io.LimitReader(input, 50<<20))
	closeErr := input.Close()
	if err != nil || closeErr != nil {
		return segment.Result{}, invalid()
	}
	output, err := os.CreateTemp("", "htr-kraken-native-*.json")
	if err != nil {
		return segment.Result{}, invalid()
	}
	defer os.Remove(output.Name())
	if err = output.Close(); err != nil {
		return segment.Result{}, invalid()
	}
	cmd := exec.CommandContext(ctx, "kraken", "--threads", "1", "--no-subline-segmentation", "-t", o.TemplatePath, "-i", path, output.Name(), "segment", "-bl", "-i", o.SegmentationModel, "ocr", "-m", o.RecognitionModel)
	var stderr stderrTail
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err = cmd.Run(); err != nil {
		return segment.Result{}, stderr.failure(ctx, err, path, output.Name(), o.SegmentationModel, o.RecognitionModel, o.TemplatePath)
	}
	input, err = os.Open(output.Name())
	if err != nil {
		return segment.Result{}, invalid()
	}
	raw, err := io.ReadAll(io.LimitReader(input, (16<<20)+1))
	closeErr = input.Close()
	if err != nil || closeErr != nil || len(raw) > 16<<20 {
		return segment.Result{}, invalid()
	}
	page, err := ParseOCR(raw, image.Pt(cfg.Width, cfg.Height))
	if err != nil {
		return segment.Result{}, err
	}
	return segment.Result{Page: page, Raw: raw, RawFormat: "kraken-ocr-json", EffectiveModel: o.RecognitionModel}, nil
}

type nativeEntity struct {
	ID, Type, Text     string
	Boundary, Baseline [][]int
	Cuts               [][][]float64
	Confidences        []float64
	Lines              []nativeEntity
}

// ParseOCR reads native Kraken entities and applies the first declared line order.
// Zero image dimensions are inferred for offline native-output conversion.
func ParseOCR(raw []byte, size image.Point) (ocrpage.Page, error) {
	if len(raw) > 16<<20 {
		return ocrpage.Page{}, invalid()
	}
	var doc struct {
		Entities []nativeEntity
		Orders   [][]string `json:"line_orders"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Entities == nil {
		return ocrpage.Page{}, invalid()
	}
	var entities []nativeEntity
	for _, e := range doc.Entities {
		switch e.Type {
		case "line":
			entities = append(entities, e)
		case "region":
			entities = append(entities, e.Lines...)
		}
	}
	if len(entities) > 100_000 {
		return ocrpage.Page{}, invalid()
	}
	byID := map[string]nativeEntity{}
	for _, e := range entities {
		if _, exists := byID[e.ID]; exists && e.ID != "" {
			return ocrpage.Page{}, invalid()
		}
		byID[e.ID] = e
	}
	if len(doc.Orders) > 0 && len(doc.Orders[0]) > 0 {
		var ordered []nativeEntity
		seen := map[string]bool{}
		for _, id := range doc.Orders[0] {
			e, ok := byID[id]
			if !ok || seen[id] {
				return ocrpage.Page{}, invalid()
			}
			ordered = append(ordered, e)
			seen[id] = true
		}
		for _, e := range entities {
			if !seen[e.ID] {
				ordered = append(ordered, e)
			}
		}
		entities = ordered
	}
	points := func(values [][]int) ([]ocrpage.Point, error) {
		out := make([]ocrpage.Point, len(values))
		for i, p := range values {
			if len(p) != 2 {
				return nil, invalid()
			}
			out[i] = ocrpage.Point{X: p[0], Y: p[1]}
		}
		return out, nil
	}
	lines := make([]ocrpage.Line, 0, len(entities))
	width, height := size.X, size.Y
	for _, e := range entities {
		polygon, err := points(e.Boundary)
		if err != nil {
			return ocrpage.Page{}, err
		}
		baseline, err := points(e.Baseline)
		if err != nil {
			return ocrpage.Page{}, err
		}
		box := ocrpage.BoundingBox(polygon)
		if box.Area() <= 0 {
			return ocrpage.Page{}, invalid()
		}
		text := []rune(e.Text)
		if len(text) != len(e.Cuts) || len(text) != len(e.Confidences) {
			return ocrpage.Page{}, invalid()
		}
		line := ocrpage.Line{ID: e.ID, Box: box, Polygon: polygon, Baseline: baseline, Text: e.Text}
		for _, c := range e.Confidences {
			if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
				return ocrpage.Page{}, invalid()
			}
			line.Confidence += c
		}
		if len(text) > 0 {
			line.Confidence /= float64(len(text))
		}
		for start := 0; start < len(text); {
			if unicode.IsSpace(text[start]) {
				start++
				continue
			}
			end := start + 1
			for end < len(text) && !unicode.IsSpace(text[end]) {
				end++
			}
			var cutPoints []ocrpage.Point
			confidence := 0.0
			for i := start; i < end; i++ {
				confidence += e.Confidences[i]
				for _, p := range e.Cuts[i] {
					if len(p) != 2 || math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsInf(p[0], 0) || math.IsInf(p[1], 0) || p[0] < 0 || p[1] < 0 || p[0] > 1_000_000 || p[1] > 1_000_000 {
						return ocrpage.Page{}, invalid()
					}
					cutPoints = append(cutPoints, ocrpage.Point{X: int(math.Floor(p[0])), Y: int(math.Floor(p[1]))}, ocrpage.Point{X: int(math.Ceil(p[0])), Y: int(math.Ceil(p[1]))})
				}
			}
			wordBox := ocrpage.BoundingBox(cutPoints).Intersect(box)
			if wordBox.Area() <= 0 {
				return ocrpage.Page{}, invalid()
			}
			line.Words = append(line.Words, ocrpage.Word{Text: string(text[start:end]), Box: wordBox, Confidence: confidence / float64(end-start)})
			start = end
		}
		if size.X == 0 {
			width = max(width, box.X2)
		}
		if size.Y == 0 {
			height = max(height, box.Y2)
		}
		lines = append(lines, line)
	}
	page := ocrpage.FromLines(width, height, lines)
	if err := ocrpage.Validate(page, ocrpage.Limits{}); err != nil {
		return ocrpage.Page{}, invalid()
	}
	return page, nil
}
