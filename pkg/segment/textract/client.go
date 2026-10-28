// Package textract provides AWS Textract table, field and layout geometry.
package textract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	sdk "github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"
	"github.com/aws/smithy-go"
	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/lehigh-university-libraries/htr/pkg/segment"
)

// API is the SDK operation injected by tests or an application's AWS configuration.
type API interface {
	AnalyzeDocument(context.Context, *sdk.AnalyzeDocumentInput, ...func(*sdk.Options)) (*sdk.AnalyzeDocumentOutput, error)
}

// Options controls credentials through an injected SDK client or the AWS chain.
type Options struct {
	API                             API
	Region                          string
	HTTPClient                      *http.Client
	Timeout                         time.Duration
	MaxImageBytes, MaxResponseBytes int64
	Features                        []types.FeatureType
}

// Client uses the AWS SDK for SigV4 and the standard credential chain.
// These dependencies avoid implementing credential refresh or signing ourselves.
type Client struct {
	api                       API
	timeout                   time.Duration
	imageLimit, responseLimit int64
	features                  []types.FeatureType
}
type boundedTransport struct {
	base  http.RoundTripper
	limit int64
}

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, e := t.base.RoundTrip(r)
	if e == nil {
		res.Body = http.MaxBytesReader(nil, res.Body, t.limit)
	}
	return res, e
}

// NewClient creates a client, loading the SDK credential chain only when API is nil.
func NewClient(ctx context.Context, o Options) (*Client, error) {
	if o.MaxImageBytes < 0 || o.MaxResponseBytes < 0 {
		return nil, invalid(providers.ErrorInvalidRequest)
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.MaxImageBytes == 0 || o.MaxImageBytes > 10<<20 {
		o.MaxImageBytes = 10 << 20
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 16 << 20
	}
	for _, f := range o.Features {
		if f != types.FeatureTypeLayout && f != types.FeatureTypeTables && f != types.FeatureTypeForms && f != types.FeatureTypeSignatures {
			return nil, invalid(providers.ErrorInvalidRequest)
		}
	}
	if o.API == nil {
		h := httpclient.Secure(o.HTTPClient, o.Timeout)
		base := h.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		h.Transport = boundedTransport{base, o.MaxResponseBytes}
		cfg, e := config.LoadDefaultConfig(ctx, config.WithRegion(o.Region), config.WithHTTPClient(h))
		if e != nil {
			return nil, providers.ErrorForAuthentication(ctx, e)
		}
		o.API = sdk.NewFromConfig(cfg)
	}
	return &Client{o.API, o.Timeout, o.MaxImageBytes, o.MaxResponseBytes, append([]types.FeatureType(nil), o.Features...)}, nil
}
func invalid(k providers.ErrorKind) error { return providers.NewError(k, 0, false, nil) }

// Name returns the registry name.
func (c *Client) Name() string { return "textract" }

// Capabilities declares all supported geometry and structured content.
func (c *Client) Capabilities(string) segment.Capabilities {
	return segment.Capabilities{Levels: []ocrpage.Level{ocrpage.LevelRegion, ocrpage.LevelLine, ocrpage.LevelWord}, Polygons: true, RegionTypes: true, ReadingOrder: true, Text: true, Confidence: true, Tables: true, Fields: true}
}

// Features returns profile-dependent AnalyzeDocument features.
func Features(profile string) []types.FeatureType {
	f := []types.FeatureType{types.FeatureTypeLayout}
	switch profile {
	case "", "generic", "ledger":
		f = append(f, types.FeatureTypeTables)
	case "form":
		f = append(f, types.FeatureTypeTables, types.FeatureTypeForms, types.FeatureTypeSignatures)
	case "letter":
		f = append(f, types.FeatureTypeSignatures)
	}
	return f
}

// Segment validates synchronous image limits and maps ordered AWS blocks.
func (c *Client) Segment(ctx context.Context, r segment.Request) (segment.Result, error) {
	if e := providers.ValidateImage(r.Image, c.imageLimit); e != nil {
		return segment.Result{}, e
	}
	media, _ := providers.CanonicalMediaType(r.Image.MediaType)
	if media != "image/jpeg" && media != "image/png" {
		return segment.Result{}, invalid(providers.ErrorInvalidRequest)
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(r.Image.Data))
	if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 10000 || cfg.Height > 10000 {
		return segment.Result{}, invalid(providers.ErrorInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	features := c.features
	if len(features) == 0 {
		features = Features(r.Hints.DocumentType)
	}
	out, e := c.api.AnalyzeDocument(ctx, &sdk.AnalyzeDocumentInput{Document: &types.Document{Bytes: r.Image.Data}, FeatureTypes: features})
	if e != nil {
		var ae smithy.APIError
		if errors.As(e, &ae) {
			switch ae.ErrorCode() {
			case "AccessDeniedException", "UnrecognizedClientException", "InvalidSignatureException":
				return segment.Result{}, invalid(providers.ErrorAuthentication)
			case "ProvisionedThroughputExceededException", "ThrottlingException":
				return segment.Result{}, providers.NewError(providers.ErrorRateLimited, 0, true, nil)
			case "InvalidParameterException", "UnsupportedDocumentException", "DocumentTooLargeException", "BadDocumentException":
				return segment.Result{}, invalid(providers.ErrorInvalidRequest)
			}
		}
		return segment.Result{}, providers.ErrorForRequest(ctx, e)
	}
	if out == nil {
		return segment.Result{}, invalid(providers.ErrorInvalidResponse)
	}
	raw, e := json.Marshal(out)
	if e != nil || int64(len(raw)) > c.responseLimit {
		return segment.Result{}, invalid(providers.ErrorResponseTooLarge)
	}
	p, e := ParseTextract(raw, image.Pt(cfg.Width, cfg.Height))
	if e != nil {
		return segment.Result{}, e
	}
	return segment.Result{Page: p, Raw: raw, RawFormat: "textract-json", EffectiveModel: aws.ToString(out.AnalyzeDocumentModelVersion)}, nil
}

// Extract makes Textract usable as a transcription client.
func (c *Client) Extract(ctx context.Context, r providers.Request) (providers.Result, error) {
	v, e := c.Segment(ctx, segment.Request{Image: r.Image, Model: r.Model})
	if e != nil {
		return providers.Result{}, e
	}
	return providers.Result{Text: v.Page.PlainText(), EffectiveModel: v.EffectiveModel}, nil
}

// RegionType maps all documented LAYOUT categories.
func RegionType(s string) ocrpage.RegionType {
	switch s {
	case "LAYOUT_TEXT":
		return ocrpage.RegionText
	case "LAYOUT_TITLE", "LAYOUT_SECTION_HEADER":
		return ocrpage.RegionHeading
	case "LAYOUT_HEADER":
		return ocrpage.RegionHeader
	case "LAYOUT_FOOTER":
		return ocrpage.RegionFooter
	case "LAYOUT_PAGE_NUMBER":
		return ocrpage.RegionPageNumber
	case "LAYOUT_LIST":
		return ocrpage.RegionList
	case "LAYOUT_FIGURE":
		return ocrpage.RegionImage
	case "LAYOUT_TABLE":
		return ocrpage.RegionTable
	case "LAYOUT_KEY_VALUE":
		return ocrpage.RegionField
	}
	return ocrpage.RegionUnknown
}

// ParseTextract parses native AnalyzeDocument JSON without network access.
func ParseTextract(raw []byte, size image.Point) (ocrpage.Page, error) {
	if len(raw) > 16<<20 || size.X <= 0 || size.Y <= 0 {
		return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
	}
	var out sdk.AnalyzeDocumentOutput
	if e := json.Unmarshal(raw, &out); e != nil || len(out.Blocks) > 100_000 {
		return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
	}
	byID := map[string]types.Block{}
	for _, b := range out.Blocks {
		id := aws.ToString(b.Id)
		if id == "" {
			return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
		}
		if _, ok := byID[id]; ok {
			return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
		}
		if b.Page != nil && *b.Page != 1 {
			return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
		}
		byID[id] = b
	}
	children := func(b types.Block, kind types.RelationshipType) []string {
		var ids []string
		for _, r := range b.Relationships {
			if r.Type == kind {
				ids = append(ids, r.Ids...)
			}
		}
		return ids
	}
	geometry := func(b types.Block) (ocrpage.Rect, []ocrpage.Point, bool) {
		if b.Geometry == nil || b.Geometry.BoundingBox == nil {
			return ocrpage.Rect{}, nil, false
		}
		bb := b.Geometry.BoundingBox
		finite := func(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }
		pixel := func(v float64, extent int) int { return int(math.Round(max(0, min(1, v)) * float64(extent))) }
		x, y, w, h := bb.Left, bb.Top, bb.Width, bb.Height
		if !finite(x) || !finite(y) || !finite(w) || !finite(h) || w < 0 || h < 0 {
			return ocrpage.Rect{}, nil, false
		}
		rect := ocrpage.Rect{X1: pixel(float64(x), size.X), Y1: pixel(float64(y), size.Y), X2: pixel(float64(x)+float64(w), size.X), Y2: pixel(float64(y)+float64(h), size.Y)}
		var poly []ocrpage.Point
		for _, p := range b.Geometry.Polygon {
			px, py := p.X, p.Y
			if !finite(px) || !finite(py) {
				return rect, nil, false
			}
			poly = append(poly, ocrpage.Point{X: pixel(float64(px), size.X), Y: pixel(float64(py), size.Y)})
		}
		return rect, poly, rect.Area() > 0
	}
	lines := map[string]ocrpage.Line{}
	for _, b := range out.Blocks {
		if b.BlockType != types.BlockTypeLine {
			continue
		}
		box, poly, ok := geometry(b)
		if !ok {
			return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
		}
		l := ocrpage.Line{ID: aws.ToString(b.Id), Text: aws.ToString(b.Text), Box: box, Polygon: poly, Confidence: float64(aws.ToFloat32(b.Confidence)) / 100}
		if b.TextType != "" {
			hand := b.TextType == types.TextTypeHandwriting
			l.Handwritten = &hand
		}
		for _, id := range children(b, types.RelationshipTypeChild) {
			wb, ok := byID[id]
			if !ok {
				return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
			}
			if wb.BlockType != types.BlockTypeWord {
				continue
			}
			box, poly, ok := geometry(wb)
			if !ok {
				return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
			}
			l.Words = append(l.Words, ocrpage.Word{ID: id, Text: aws.ToString(wb.Text), Box: box, Polygon: poly, Confidence: float64(aws.ToFloat32(wb.Confidence)) / 100})
		}
		lines[l.ID] = l
	}
	var descendants func(string, map[string]bool, int) ([]string, error)
	visits := 0
	descendants = func(id string, seen map[string]bool, depth int) ([]string, error) {
		visits++
		if depth > 256 || seen[id] || visits > 1_000_000 {
			return nil, invalid(providers.ErrorInvalidResponse)
		}
		b, ok := byID[id]
		if !ok {
			return nil, invalid(providers.ErrorInvalidResponse)
		}
		if b.BlockType == types.BlockTypeLine {
			return []string{id}, nil
		}
		seen[id] = true
		defer delete(seen, id)
		var ids []string
		for _, child := range children(b, types.RelationshipTypeChild) {
			v, e := descendants(child, seen, depth+1)
			if e != nil {
				return nil, e
			}
			ids = append(ids, v...)
		}
		return ids, nil
	}
	p := ocrpage.Page{Width: size.X, Height: size.Y}
	used := map[string]bool{}
	for _, b := range out.Blocks {
		kind := string(b.BlockType)
		if !strings.HasPrefix(kind, "LAYOUT_") {
			continue
		}
		box, poly, ok := geometry(b)
		if !ok {
			return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
		}
		r := ocrpage.Region{ID: aws.ToString(b.Id), Type: RegionType(kind), SourceType: kind, Box: box, Polygon: poly, Confidence: float64(aws.ToFloat32(b.Confidence)) / 100}
		ids, e := descendants(r.ID, map[string]bool{}, 0)
		if e != nil {
			return ocrpage.Page{}, e
		}
		for _, id := range ids {
			if !used[id] {
				r.Lines = append(r.Lines, lines[id])
				used[id] = true
			}
		}
		p.Regions = append(p.Regions, r)
	}
	var loose []ocrpage.Line
	for _, b := range out.Blocks {
		id := aws.ToString(b.Id)
		if l, ok := lines[id]; ok && !used[id] {
			loose = append(loose, l)
		}
	}
	if len(loose) > 0 {
		p.Regions = append(p.Regions, ocrpage.FromLines(size.X, size.Y, loose).Regions...)
	}
	lineIDs := func(ids []string) []string {
		var result []string
		for _, line := range p.Lines() {
			for _, word := range line.Words {
				if slices.Contains(ids, word.ID) {
					result = append(result, line.ID)
					break
				}
			}
		}
		return result
	}
	structuredRegion := func(b types.Block, typ ocrpage.RegionType) (int, error) {
		box, poly, ok := geometry(b)
		if !ok {
			return 0, invalid(providers.ErrorInvalidResponse)
		}
		best, mass := -1, 0.0
		for i, r := range p.Regions {
			if r.Type != typ || r.Implicit {
				continue
			}
			a := box.Intersect(r.Box).Area()
			if a > mass {
				best, mass = i, a
			}
		}
		if best < 0 {
			best = len(p.Regions)
			p.Regions = append(p.Regions, ocrpage.Region{ID: aws.ToString(b.Id), Type: typ, Box: box, Polygon: poly})
		}
		return best, nil
	}
	for _, b := range out.Blocks {
		if b.BlockType == types.BlockTypeTable {
			idx, err := structuredRegion(b, ocrpage.RegionTable)
			if err != nil {
				return ocrpage.Page{}, err
			}
			table := &ocrpage.Table{}
			for _, id := range children(b, types.RelationshipTypeChild) {
				cell, ok := byID[id]
				if !ok {
					return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
				}
				if cell.BlockType != types.BlockTypeCell {
					continue
				}
				box, _, ok := geometry(cell)
				if !ok {
					return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
				}
				c := ocrpage.Cell{Row: int(aws.ToInt32(cell.RowIndex)) - 1, Col: int(aws.ToInt32(cell.ColumnIndex)) - 1,
					RowSpan: max(1, int(aws.ToInt32(cell.RowSpan))), ColSpan: max(1, int(aws.ToInt32(cell.ColumnSpan))), Box: box,
					LineIDs: lineIDs(children(cell, types.RelationshipTypeChild))}
				table.Rows = max(table.Rows, c.Row+c.RowSpan)
				table.Cols = max(table.Cols, c.Col+c.ColSpan)
				table.Cells = append(table.Cells, c)
			}
			p.Regions[idx].Table = table
		}
		if b.BlockType == types.BlockTypeKeyValueSet && slices.Contains(b.EntityTypes, types.EntityTypeKey) {
			idx, err := structuredRegion(b, ocrpage.RegionField)
			if err != nil {
				return ocrpage.Page{}, err
			}
			field := &ocrpage.Field{KeyLineIDs: lineIDs(children(b, types.RelationshipTypeChild))}
			for _, id := range children(b, types.RelationshipTypeValue) {
				value, ok := byID[id]
				if !ok {
					return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
				}
				field.ValueLineIDs = append(field.ValueLineIDs, lineIDs(children(value, types.RelationshipTypeChild))...)
			}
			p.Regions[idx].Field = field
		}
	}
	if e := ocrpage.Validate(p, ocrpage.Limits{}); e != nil {
		return ocrpage.Page{}, invalid(providers.ErrorInvalidResponse)
	}
	return p, nil
}
