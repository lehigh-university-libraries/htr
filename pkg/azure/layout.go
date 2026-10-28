package azure

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// LayoutOptions injects explicit Azure credentials, endpoint and transport.
// The shared analyze path limits responses to 16 MiB and rejects cross-origin polling.
type LayoutOptions struct {
	Endpoint              string
	APIKey                func(context.Context) (string, error)
	HTTPClient            *http.Client
	Timeout, PollInterval time.Duration
	MaxImageBytes         int64
}

// Segmenter exposes prebuilt-layout geometry and text.
type Segmenter struct{ options LayoutOptions }

// NewSegmenter constructs the Azure layout client without environment access.
func NewSegmenter(o LayoutOptions) (*Segmenter, error) {
	if _, e := httpclient.ParseEndpoint(o.Endpoint); e != nil || o.APIKey == nil || o.MaxImageBytes < 0 {
		return nil, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if o.MaxImageBytes == 0 {
		o.MaxImageBytes = 50 << 20
	}
	return &Segmenter{o}, nil
}

// Name returns the registry name.
func (s *Segmenter) Name() string { return "azure-layout" }

// Capabilities declares the geometry returned by prebuilt-layout.
func (s *Segmenter) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelRegion, ocrpage.LevelLine, ocrpage.LevelWord}, Polygons: true, ReadingOrder: true, RegionTypes: true, Text: true, Confidence: true, Tables: true}
}

// Segment analyzes an image through the existing bounded Azure poller.
func (s *Segmenter) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	if r.Model != "" && r.Model != "prebuilt-layout" {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	if e := providers.ValidateImage(r.Image, s.options.MaxImageBytes); e != nil {
		return segment.Result{}, e
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(r.Image.Data))
	if e != nil || cfg.Width < 50 || cfg.Height < 50 || cfg.Width > 10000 || cfg.Height > 10000 {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	key, e := s.options.APIKey(ctx)
	if e != nil || strings.TrimSpace(key) == "" {
		return segment.Result{}, providers.ErrorForAuthentication(ctx, e)
	}
	raw, e := analyzeImage(ctx, s.options.Endpoint, key, "prebuilt-layout", r.Image.Data, s.options.Timeout, s.options.HTTPClient, s.options.PollInterval)
	if e != nil {
		return segment.Result{}, e
	}
	p, e := ParseLayout(raw, image.Pt(cfg.Width, cfg.Height))
	if e != nil {
		return segment.Result{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
	}
	return segment.Result{Page: p, Raw: raw, RawFormat: "azure-layout-json", EffectiveModel: "prebuilt-layout"}, nil
}

// Extract makes the grounded parser usable for transcription evaluation.
func (s *Segmenter) Extract(ctx context.Context, r providers.Request) (providers.Result, error) {
	v, e := s.Segment(ctx, segment.Request{Image: r.Image, Model: r.Model})
	if e != nil {
		return providers.Result{}, e
	}
	return providers.Result{Text: v.Page.PlainText(), EffectiveModel: v.EffectiveModel, Usage: v.Usage}, nil
}

type azureSpan struct{ Offset, Length int }
type azureBounds struct {
	PageNumber int
	Polygon    []float64
}
type azureLine struct {
	Content string
	Polygon []float64
	Spans   []azureSpan
}
type azureWord struct {
	Content    string
	Polygon    []float64
	Span       azureSpan
	Confidence float64
}
type azureParagraph struct {
	Role, Content   string
	Spans           []azureSpan
	BoundingRegions []azureBounds
}
type azureTable struct {
	RowCount, ColumnCount int
	Spans                 []azureSpan
	BoundingRegions       []azureBounds
	Cells                 []struct {
		RowIndex, ColumnIndex, RowSpan, ColumnSpan int
		Spans                                      []azureSpan
		BoundingRegions                            []azureBounds
	}
}

func containsSpans(outer, inner []azureSpan) bool {
	if len(inner) == 0 {
		return false
	}
	for _, a := range inner {
		found := false
		if a.Offset < 0 || a.Length < 0 {
			return false
		}
		for _, b := range outer {
			if b.Offset >= 0 && b.Length >= 0 && a.Offset >= b.Offset && a.Offset-b.Offset <= b.Length && a.Length <= b.Length-(a.Offset-b.Offset) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ParseLayout maps the documented 2024-11-30 image result into a canonical page.
func ParseLayout(raw []byte, size image.Point) (ocrpage.Page, error) {
	bad := func() (ocrpage.Page, error) {
		return ocrpage.Page{}, providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
	}
	if len(raw) > 16<<20 {
		return bad()
	}
	type analysis struct {
		Pages []struct {
			PageNumber    int
			Width, Height float64
			Unit          string
			Lines         []azureLine
			Words         []azureWord
		}
		Paragraphs []azureParagraph
		Tables     []azureTable
		Figures    []struct {
			BoundingRegions []azureBounds
			Spans           []azureSpan
		}
	}
	var envelope struct{ AnalyzeResult *analysis }
	if e := json.Unmarshal(raw, &envelope); e != nil || envelope.AnalyzeResult == nil {
		return bad()
	}
	a := envelope.AnalyzeResult
	if len(a.Pages) != 1 {
		return bad()
	}
	ap := a.Pages[0]
	if ap.Unit != "pixel" || ap.Width <= 0 || ap.Height <= 0 {
		return bad()
	}
	width, height := int(math.Round(ap.Width)), int(math.Round(ap.Height))
	if width != size.X || height != size.Y {
		return bad()
	}
	p := ocrpage.Page{Width: width, Height: height}
	poly := func(v []float64) ([]ocrpage.Point, bool) {
		if len(v) < 6 || len(v)%2 != 0 {
			return nil, false
		}
		points := make([]ocrpage.Point, len(v)/2)
		for i := range points {
			x, y := v[2*i], v[2*i+1]
			if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
				return nil, false
			}
			points[i] = ocrpage.Point{X: int(math.Round(max(0, min(float64(width), x)))), Y: int(math.Round(max(0, min(float64(height), y))))}
		}
		return points, true
	}
	bounds := func(v []azureBounds) ([]ocrpage.Point, bool) {
		for _, b := range v {
			if b.PageNumber == ap.PageNumber {
				return poly(b.Polygon)
			}
		}
		return nil, false
	}
	firstSpan := func(spans []azureSpan) int {
		offset := math.MaxInt
		for _, span := range spans {
			if span.Offset >= 0 {
				offset = min(offset, span.Offset)
			}
		}
		return offset
	}
	offsets := map[string]int{}
	paragraphs := map[int]int{}
	for i, r := range a.Paragraphs {
		inTable := false
		for _, table := range a.Tables {
			for _, cell := range table.Cells {
				if containsSpans(cell.Spans, r.Spans) {
					inTable = true
				}
			}
		}
		if inTable {
			continue
		}
		points, ok := bounds(r.BoundingRegions)
		if !ok {
			return bad()
		}
		label := r.Role
		if label == "" {
			label = "text"
		}
		id := fmtID("paragraph", i)
		offsets[id] = firstSpan(r.Spans)
		paragraphs[i] = len(p.Regions)
		p.Regions = append(p.Regions, ocrpage.Region{ID: id, Type: ocrpage.CanonicalType(label), SourceType: r.Role, Box: ocrpage.BoundingBox(points), Polygon: points})
	}
	lines := make([]ocrpage.Line, len(ap.Lines))
	for i, l := range ap.Lines {
		points, ok := poly(l.Polygon)
		if !ok {
			return bad()
		}
		lines[i] = ocrpage.Line{ID: fmtID("line", i), Text: l.Content, Box: ocrpage.BoundingBox(points), Polygon: points}
		for j, w := range ap.Words {
			if !containsSpans(l.Spans, []azureSpan{w.Span}) {
				continue
			}
			points, ok := poly(w.Polygon)
			if !ok {
				return bad()
			}
			lines[i].Words = append(lines[i].Words, ocrpage.Word{ID: fmtID("word", j), Text: w.Content, Box: ocrpage.BoundingBox(points), Polygon: points, Confidence: w.Confidence})
		}
	}
	tableOwner := map[int]int{}
	for ti, t := range a.Tables {
		points, ok := bounds(t.BoundingRegions)
		if !ok {
			return bad()
		}
		r := ocrpage.Region{ID: fmtID("table", ti), Type: ocrpage.RegionTable, Box: ocrpage.BoundingBox(points), Polygon: points, Table: &ocrpage.Table{Rows: t.RowCount, Cols: t.ColumnCount}}
		offsets[r.ID] = firstSpan(t.Spans)
		included := map[int]bool{}
		for _, c := range t.Cells {
			offsets[r.ID] = min(offsets[r.ID], firstSpan(c.Spans))
			points, ok := bounds(c.BoundingRegions)
			if !ok {
				return bad()
			}
			cell := ocrpage.Cell{Row: c.RowIndex, Col: c.ColumnIndex, RowSpan: max(1, c.RowSpan), ColSpan: max(1, c.ColumnSpan), Box: ocrpage.BoundingBox(points)}
			for i, l := range ap.Lines {
				if containsSpans(c.Spans, l.Spans) {
					cell.LineIDs = append(cell.LineIDs, lines[i].ID)
					tableOwner[i] = len(p.Regions)
					if !included[i] {
						included[i] = true
					}
				}
			}
			r.Table.Cells = append(r.Table.Cells, cell)
		}
		// Page lines follow document order; cell arrays need not.
		for i := range lines {
			if included[i] {
				r.Lines = append(r.Lines, lines[i])
			}
		}
		p.Regions = append(p.Regions, r)
	}
	for i, l := range lines {
		if _, ok := tableOwner[i]; ok {
			continue
		}
		idx := -1
		for j, r := range a.Paragraphs {
			if pi, exists := paragraphs[j]; exists && containsSpans(r.Spans, ap.Lines[i].Spans) {
				idx = pi
				break
			}
		}
		if idx < 0 {
			idx = len(p.Regions)
			id := fmtID("implicit", i)
			offsets[id] = firstSpan(ap.Lines[i].Spans)
			p.Regions = append(p.Regions, ocrpage.Region{ID: id, Type: ocrpage.RegionText, Implicit: true})
		}
		p.Regions[idx].Lines = append(p.Regions[idx].Lines, l)
		if p.Regions[idx].Implicit {
			p.Regions[idx].Box = p.Regions[idx].Box.Union(l.Box)
		}
	}
	for i, f := range a.Figures {
		points, ok := bounds(f.BoundingRegions)
		if !ok {
			return bad()
		}
		p.Regions = append(p.Regions, ocrpage.Region{ID: fmtID("figure", i), Type: ocrpage.RegionImage, Box: ocrpage.BoundingBox(points), Polygon: points})
		offsets[fmtID("figure", i)] = firstSpan(f.Spans)
	}
	slices.SortStableFunc(p.Regions, func(a, b ocrpage.Region) int {
		return cmp.Compare(offsets[a.ID], offsets[b.ID])
	})
	if e := ocrpage.Validate(p, ocrpage.Limits{}); e != nil {
		return bad()
	}
	return p, nil
}
func fmtID(prefix string, i int) string { return prefix + "_" + strconv.Itoa(i+1) }
