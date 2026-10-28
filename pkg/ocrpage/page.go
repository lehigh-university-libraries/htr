// Package ocrpage provides a provider-neutral, ordered OCR page and format readers.
package ocrpage

import (
	"fmt"
	"math"
	"strings"
)

// Point is an image-space coordinate.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect is a rectangle with exclusive maximum edges.
type Rect struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

// Area returns the rectangle's nonnegative area.
func (r Rect) Area() float64 { return float64(max(0, r.X2-r.X1)) * float64(max(0, r.Y2-r.Y1)) }

// Intersect returns the intersection, or an empty rectangle.
func (r Rect) Intersect(s Rect) Rect {
	t := Rect{max(r.X1, s.X1), max(r.Y1, s.Y1), min(r.X2, s.X2), min(r.Y2, s.Y2)}
	if t.X2 < t.X1 || t.Y2 < t.Y1 {
		return Rect{}
	}
	return t
}

// Union returns the enclosing rectangle.
func (r Rect) Union(s Rect) Rect {
	if r.Area() == 0 {
		return s
	}
	if s.Area() == 0 {
		return r
	}
	return Rect{min(r.X1, s.X1), min(r.Y1, s.Y1), max(r.X2, s.X2), max(r.Y2, s.Y2)}
}

// Polygon returns the four corners in clockwise order.
func (r Rect) Polygon() []Point {
	return []Point{{r.X1, r.Y1}, {r.X2, r.Y1}, {r.X2, r.Y2}, {r.X1, r.Y2}}
}

// BoundingBox encloses a set of points.
func BoundingBox(p []Point) Rect {
	if len(p) == 0 {
		return Rect{}
	}
	r := Rect{p[0].X, p[0].Y, p[0].X, p[0].Y}
	for _, v := range p[1:] {
		r.X1 = min(r.X1, v.X)
		r.Y1 = min(r.Y1, v.Y)
		r.X2 = max(r.X2, v.X)
		r.Y2 = max(r.Y2, v.Y)
	}
	return r
}

// Level selects an explicitly represented geometry level.
type Level string

// Supported geometry levels.
const (
	LevelRegion Level = "region"
	LevelLine   Level = "line"
	LevelWord   Level = "word"
)

// Format identifies an interchange format.
type Format string

// Supported interchange formats.
const (
	FormatHOCR    Format = "hocr"
	FormatPageXML Format = "pagexml"
	FormatALTO    Format = "alto"
	FormatIIIF    Format = "iiif"
	FormatKraken  Format = "kraken"
	FormatHTRJSON Format = "htrjson"
	FormatText    Format = "txt"
)

// RegionType is a PAGE-aligned canonical region category.
type RegionType string

// Canonical region categories.
const (
	RegionText        RegionType = "text"
	RegionHeading     RegionType = "heading"
	RegionCaption     RegionType = "caption"
	RegionHeader      RegionType = "header"
	RegionFooter      RegionType = "footer"
	RegionPageNumber  RegionType = "page-number"
	RegionMarginalia  RegionType = "marginalia"
	RegionFootnote    RegionType = "footnote"
	RegionList        RegionType = "list"
	RegionDropCapital RegionType = "drop-capital"
	RegionSignature   RegionType = "signature"
	RegionTable       RegionType = "table"
	RegionField       RegionType = "field"
	RegionImage       RegionType = "image"
	RegionMath        RegionType = "math"
	RegionSeparator   RegionType = "separator"
	RegionAdvert      RegionType = "advert"
	RegionNoise       RegionType = "noise"
	RegionUnknown     RegionType = "unknown"
)

// CanonicalType normalizes a format label, retaining unknown labels in SourceType at call sites.
func CanonicalType(s string) RegionType {
	switch s {
	case "title", "sectionHeading":
		return RegionHeading
	case "pageHeader":
		return RegionHeader
	case "pageFooter":
		return RegionFooter
	case "pageNumber":
		return RegionPageNumber
	case "paragraph", "body":
		return RegionText
	}
	for _, t := range []RegionType{RegionText, RegionHeading, RegionCaption, RegionHeader, RegionFooter, RegionPageNumber, RegionMarginalia, RegionFootnote, RegionList, RegionDropCapital, RegionSignature, RegionTable, RegionField, RegionImage, RegionMath, RegionSeparator, RegionAdvert, RegionNoise} {
		if s == string(t) {
			return t
		}
	}
	return RegionUnknown
}

// Provenance records optional model and creation metadata.
type Provenance struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Created  string `json:"created,omitempty"`
}

// Page contains regions in reading order.
type Page struct {
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	Regions    []Region   `json:"regions"`
	Source     Format     `json:"source,omitempty"`
	Provenance Provenance `json:"provenance,omitempty"`
}

// Region groups lines and optional structured content.
type Region struct {
	ID         string     `json:"id,omitempty"`
	Type       RegionType `json:"type"`
	SourceType string     `json:"sourceType,omitempty"`
	Box        Rect       `json:"box"`
	Polygon    []Point    `json:"polygon,omitempty"`
	Lines      []Line     `json:"lines,omitempty"`
	Table      *Table     `json:"table,omitempty"`
	Field      *Field     `json:"field,omitempty"`
	Implicit   bool       `json:"implicit,omitempty"`
	Confidence float64    `json:"confidence,omitempty"`
}

// Line contains word geometry or a line-level transcription.
type Line struct {
	ID              string  `json:"id,omitempty"`
	Box             Rect    `json:"box"`
	Polygon         []Point `json:"polygon,omitempty"`
	Baseline        []Point `json:"baseline,omitempty"`
	BaselineDerived bool    `json:"baselineDerived,omitempty"`
	Words           []Word  `json:"words,omitempty"`
	Text            string  `json:"text,omitempty"`
	Handwritten     *bool   `json:"handwritten,omitempty"`
	Confidence      float64 `json:"confidence,omitempty"`
}

// Word is one transcribed word and its geometry.
type Word struct {
	ID         string  `json:"id,omitempty"`
	Text       string  `json:"text,omitempty"`
	Box        Rect    `json:"box"`
	Polygon    []Point `json:"polygon,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Table stores cell structure without imposing a scoring method.
type Table struct {
	Rows  int    `json:"rows"`
	Cols  int    `json:"cols"`
	Cells []Cell `json:"cells"`
}

// Cell is a zero-based table cell, with spans and line references.
type Cell struct {
	Row     int      `json:"row"`
	Col     int      `json:"col"`
	RowSpan int      `json:"rowSpan"`
	ColSpan int      `json:"colSpan"`
	Box     Rect     `json:"box"`
	LineIDs []string `json:"lineIDs,omitempty"`
}

// Field preserves references to form key and value lines.
type Field struct {
	KeyLineIDs   []string `json:"keyLineIDs"`
	ValueLineIDs []string `json:"valueLineIDs"`
}

// Element is a flattened geometry used by evaluators.
type Element struct {
	ID                string
	Type              RegionType
	Text              string
	Box               Rect
	Polygon, Baseline []Point
	BaselineDerived   bool
	Order             int
}

// PlainText returns word text when words exist, otherwise line text.
func (l Line) PlainText() string {
	if len(l.Words) == 0 {
		return l.Text
	}
	s := make([]string, len(l.Words))
	for i, w := range l.Words {
		s[i] = w.Text
	}
	return strings.Join(s, " ")
}

// Text returns line text in reading order.
func (r Region) Text() string {
	s := make([]string, len(r.Lines))
	for i, l := range r.Lines {
		s[i] = l.PlainText()
	}
	return strings.Join(s, "\n")
}

// PlainText returns region text in reading order.
func (p Page) PlainText() string {
	var s []string
	for _, r := range p.Regions {
		if len(r.Lines) > 0 {
			s = append(s, r.Text())
		}
	}
	return strings.Join(s, "\n")
}

// Lines returns all lines in reading order.
func (p Page) Lines() []Line {
	var v []Line
	for _, r := range p.Regions {
		v = append(v, r.Lines...)
	}
	return v
}

// Words returns all words in reading order.
func (p Page) Words() []Word {
	var v []Word
	for _, l := range p.Lines() {
		v = append(v, l.Words...)
	}
	return v
}

// Elements returns explicitly represented elements; implicit regions are skipped.
func (p Page) Elements(level Level) []Element {
	var v []Element
	for _, r := range p.Regions {
		switch level {
		case LevelRegion:
			if !r.Implicit {
				v = append(v, Element{ID: r.ID, Type: r.Type, Text: r.Text(), Box: r.Box, Polygon: r.Polygon})
			}
		case LevelLine:
			for _, l := range r.Lines {
				v = append(v, Element{ID: l.ID, Type: r.Type, Text: l.PlainText(), Box: l.Box, Polygon: l.Polygon, Baseline: l.Baseline, BaselineDerived: l.BaselineDerived})
			}
		case LevelWord:
			for _, l := range r.Lines {
				for _, w := range l.Words {
					v = append(v, Element{ID: w.ID, Type: r.Type, Text: w.Text, Box: w.Box, Polygon: w.Polygon})
				}
			}
		}
	}
	for i := range v {
		v[i].Order = i
	}
	return v
}

// HasLevel reports whether there is explicit geometry at a level.
func (p Page) HasLevel(l Level) bool {
	for _, e := range p.Elements(l) {
		if e.Box.Area() > 0 {
			return true
		}
	}
	return false
}

// FromLines wraps lines in one implicit region.
func FromLines(w, h int, lines []Line) Page {
	var b Rect
	for _, l := range lines {
		b = b.Union(l.Box)
	}
	return Page{Width: w, Height: h, Regions: []Region{{Type: RegionText, Box: b, Lines: lines, Implicit: true}}}
}

// Scale returns an independent page with all coordinates scaled to the new dimensions.
func (p Page) Scale(w, h int) Page {
	if p.Width <= 0 || p.Height <= 0 {
		return p
	}
	sx, sy := float64(w)/float64(p.Width), float64(h)/float64(p.Height)
	point := func(v Point) Point {
		return Point{int(math.Round(float64(v.X) * sx)), int(math.Round(float64(v.Y) * sy))}
	}
	rect := func(r Rect) Rect {
		a, b := point(Point{r.X1, r.Y1}), point(Point{r.X2, r.Y2})
		return Rect{a.X, a.Y, b.X, b.Y}
	}
	points := func(v []Point) []Point {
		if v == nil {
			return nil
		}
		o := make([]Point, len(v))
		for i, x := range v {
			o[i] = point(x)
		}
		return o
	}
	p.Regions = append([]Region(nil), p.Regions...)
	for i := range p.Regions {
		r := &p.Regions[i]
		r.Box = rect(r.Box)
		r.Polygon = points(r.Polygon)
		r.Lines = append([]Line(nil), r.Lines...)
		for j := range r.Lines {
			l := &r.Lines[j]
			if l.Handwritten != nil {
				v := *l.Handwritten
				l.Handwritten = &v
			}
			l.Box = rect(l.Box)
			l.Polygon = points(l.Polygon)
			l.Baseline = points(l.Baseline)
			l.Words = append([]Word(nil), l.Words...)
			for k := range l.Words {
				l.Words[k].Box = rect(l.Words[k].Box)
				l.Words[k].Polygon = points(l.Words[k].Polygon)
			}
		}
		if r.Table != nil {
			t := *r.Table
			t.Cells = append([]Cell(nil), t.Cells...)
			for j := range t.Cells {
				t.Cells[j].Box = rect(t.Cells[j].Box)
				t.Cells[j].LineIDs = append([]string(nil), t.Cells[j].LineIDs...)
			}
			r.Table = &t
		}
		if r.Field != nil {
			f := *r.Field
			f.KeyLineIDs = append([]string(nil), f.KeyLineIDs...)
			f.ValueLineIDs = append([]string(nil), f.ValueLineIDs...)
			r.Field = &f
		}
	}
	p.Width, p.Height = w, h
	return p
}

// CheckLevel validates a geometry level.
func CheckLevel(l Level) error {
	if l != LevelRegion && l != LevelLine && l != LevelWord {
		return fmt.Errorf("ocrpage: invalid level %q", l)
	}
	return nil
}
