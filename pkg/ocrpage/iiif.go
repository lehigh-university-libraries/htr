package ocrpage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// IIIFReadOptions supplies image dimensions absent from AnnotationPages.
type IIIFReadOptions struct {
	CanvasWidth, CanvasHeight int
	Limits                    Limits
	// InferWords enables Scribe's legacy text-export crosswalk. Inferred word
	// geometry must not be used as segmentation ground truth.
	InferWords bool
	// SkipEmptyText retains the legacy export behavior for non-textual segments.
	SkipEmptyText bool
}

// Skip explains an annotation that could not be interpreted as OCR.
type Skip struct {
	Index  int
	Reason string
}

// IIIFReadResult includes diagnostics for non-OCR and malformed annotations.
type IIIFReadResult struct {
	Page    Page
	Ignored int
	Skipped []Skip
}

// ParseXYWH parses pixel fragments with rounded coordinates; percent coordinates are rejected.
func ParseXYWH(s string) (Rect, error) {
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[i+1:]
	}
	if strings.Contains(s, "=") {
		found := false
		for _, parameter := range strings.Split(s, "&") {
			key, value, ok := strings.Cut(parameter, "=")
			if !ok || strings.TrimSpace(key) != "xywh" {
				continue
			}
			if found {
				return Rect{}, errors.New("ocrpage: ambiguous xywh")
			}
			found = true
			s = strings.TrimSpace(value)
		}
		if !found {
			return Rect{}, errors.New("ocrpage: missing xywh")
		}
	}
	s = strings.TrimPrefix(s, "xywh=")
	s = strings.TrimPrefix(s, "pixel:")
	v, e := numbers(s)
	if e != nil || len(v) != 4 {
		return Rect{}, errors.New("ocrpage: invalid xywh")
	}
	if v[0] < 0 || v[1] < 0 || v[2] <= 0 || v[3] <= 0 {
		return Rect{}, errors.New("ocrpage: invalid xywh extents")
	}
	x, y, w, h := int(math.Round(v[0])), int(math.Round(v[1])), int(math.Round(v[2])), int(math.Round(v[3]))
	r := Rect{x, y, x + w, y + h}
	if w <= 0 || h <= 0 || r.X2 > 1_000_000 || r.Y2 > 1_000_000 {
		return Rect{}, errors.New("ocrpage: invalid xywh extents")
	}
	return r, nil
}
func stringValue(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// NormalizeAnnotation returns a shallow normalized W3C annotation without mutating the input.
func NormalizeAnnotation(a map[string]any, canvas string) map[string]any {
	n := make(map[string]any, len(a))
	for k, v := range a {
		n[k] = v
	}
	if n["id"] == nil {
		n["id"] = n["@id"]
	}
	if n["type"] == nil {
		n["type"] = "Annotation"
	}
	if n["body"] == nil {
		if r, ok := n["resource"].(map[string]any); ok {
			n["body"] = map[string]any{"type": "TextualBody", "value": stringValue(r, "chars"), "purpose": "transcribing"}
		} else if s := stringValue(n, "bodyValue"); s != "" {
			n["body"] = map[string]any{"type": "TextualBody", "value": s, "purpose": "transcribing"}
		}
	}
	if s, ok := n["body"].(string); ok {
		n["body"] = map[string]any{"type": "TextualBody", "value": s, "purpose": "transcribing"}
	}
	if n["target"] == nil {
		n["target"] = n["on"]
	}
	if s, ok := n["target"].(string); ok {
		parts := strings.SplitN(s, "#", 2)
		t := map[string]any{"source": parts[0]}
		if t["source"] == "" {
			t["source"] = canvas
		}
		if len(parts) > 1 {
			t["selector"] = map[string]any{"type": "FragmentSelector", "value": parts[1]}
		}
		n["target"] = t
	}
	return n
}
func bodies(v any) []map[string]any {
	switch b := v.(type) {
	case map[string]any:
		return []map[string]any{b}
	case []any:
		var out []map[string]any
		for _, v := range b {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
func annotationText(a map[string]any) (string, RegionType) {
	var text []string
	typ := RegionUnknown
	for _, b := range bodies(a["body"]) {
		purpose := stringValue(b, "purpose")
		switch purpose {
		case "classifying":
			typ = CanonicalType(stringValue(b, "value"))
		case "", "transcribing", "supplementing":
			text = append(text, stringValue(b, "value"))
		}
	}
	return strings.TrimSpace(strings.Join(text, " ")), typ
}
func annotationGeometry(a map[string]any) (Rect, []Point, []Point, error) {
	t, ok := a["target"].(map[string]any)
	if !ok {
		return Rect{}, nil, nil, errors.New("missing target")
	}
	var b Rect
	spatialSeen := false
	var poly, base []Point
	var selectors []any
	switch s := t["selector"].(type) {
	case map[string]any:
		selectors = []any{s}
	case []any:
		selectors = s
	}
	for _, v := range selectors {
		s, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch stringValue(s, "type") {
		case "FragmentSelector":
			value := stringValue(s, "value")
			if strings.Contains(value, "=") {
				present := false
				for _, parameter := range strings.Split(value, "&") {
					key, _, _ := strings.Cut(parameter, "=")
					present = present || strings.TrimSpace(key) == "xywh"
				}
				if !present {
					continue
				}
			}
			if spatialSeen {
				return b, nil, nil, errors.New("ocrpage: ambiguous spatial selectors")
			}
			spatialSeen = true
			var err error
			b, err = ParseXYWH(value)
			if err != nil {
				return b, nil, nil, err
			}
		case "SvgSelector":
			root, err := readXML([]byte(stringValue(s, "value")), false, Limits{MaxBytes: 1 << 20, MaxDepth: 16, MaxElements: 10000}.defaults())
			if err != nil {
				return b, nil, nil, err
			}
			for _, n := range root.all("polygon") {
				poly, err = points(n.a["points"])
				if err != nil {
					return b, nil, nil, err
				}
			}
			for _, n := range root.all("polyline") {
				if n.a["class"] == "baseline" {
					base, err = points(n.a["points"])
					if err != nil {
						return b, nil, nil, err
					}
				}
			}
		}
	}
	if b.Area() == 0 {
		return b, nil, nil, errors.New("missing fragment geometry")
	}
	return b, poly, base, nil
}

// ReadIIIF reads OCR annotations, preserving item reading order and real geometry.
func ReadIIIF(data []byte, o IIIFReadOptions) (IIIFReadResult, error) {
	var out IIIFReadResult
	l := o.Limits.defaults()
	if len(data) > l.MaxBytes {
		return out, ErrLimit
	}
	if err := checkJSON(data, l); err != nil {
		return out, err
	}
	var doc struct {
		Items         []map[string]any `json:"items"`
		Width, Height int
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Items == nil {
		return out, errors.New("ocrpage: AnnotationPage items required")
	}
	if len(doc.Items) > l.MaxElements {
		return out, ErrLimit
	}
	p := Page{Width: max(doc.Width, o.CanvasWidth), Height: max(doc.Height, o.CanvasHeight), Source: FormatIIIF}
	type candidate struct {
		line Line
		rank int
	}
	var candidates []candidate
	var words []Word
	best := 0
	for i, raw := range doc.Items {
		g := strings.ToLower(strings.TrimSpace(stringValue(raw, "textGranularity")))
		rank := 0
		switch g {
		case "page":
			rank = 1
		case "block":
			rank = 2
		case "paragraph":
			rank = 3
		case "line":
			rank = 4
		case "word", "glyph":
		default:
			out.Ignored++
			continue
		}
		a := NormalizeAnnotation(raw, "")
		text, typ := annotationText(a)
		if o.SkipEmptyText && text == "" {
			out.Skipped = append(out.Skipped, Skip{i, "missing text"})
			continue
		}
		b, poly, base, e := annotationGeometry(a)
		if e != nil && g == "page" && o.CanvasWidth > 0 && o.CanvasHeight > 0 {
			if target, ok := a["target"].(map[string]any); ok && target["selector"] == nil {
				b = Rect{X2: o.CanvasWidth, Y2: o.CanvasHeight}
				e = nil
			}
		}
		if e != nil {
			out.Skipped = append(out.Skipped, Skip{i, e.Error()})
			continue
		}
		if p.Width == 0 || o.CanvasWidth == 0 {
			p.Width = max(p.Width, b.X2)
		}
		if p.Height == 0 || o.CanvasHeight == 0 {
			p.Height = max(p.Height, b.Y2)
		}
		id := stringValue(a, "id")
		if id == "" {
			id = fmt.Sprintf("annotation_%d", i+1)
		}
		if g == "block" {
			p.Regions = append(p.Regions, Region{ID: id, Type: typ, Box: b, Polygon: poly})
		}
		if rank > 0 {
			best = max(best, rank)
			candidates = append(candidates, candidate{Line{ID: id, Box: b, Polygon: poly, Baseline: base, Text: text}, rank})
		} else {
			words = append(words, Word{ID: id, Text: text, Box: b, Polygon: poly})
		}
	}
	var lines []Line
	for _, c := range candidates {
		if c.rank == best {
			lines = append(lines, c.line)
		}
	}
	if len(lines) == 0 && len(words) > 0 {
		ordered := slices.Clone(words)
		slices.SortStableFunc(ordered, func(a, b Word) int {
			ay, by := (a.Box.Y1+a.Box.Y2)/2, (b.Box.Y1+b.Box.Y2)/2
			if ay != by {
				return ay - by
			}
			return a.Box.X1 - b.Box.X1
		})
		for _, w := range ordered {
			idx := -1
			for i := range lines {
				if absInt((lines[i].Box.Y1+lines[i].Box.Y2-w.Box.Y1-w.Box.Y2)/2) <= 20 {
					idx = i
					break
				}
			}
			if idx < 0 {
				lines = append(lines, Line{ID: fmt.Sprintf("line_%d", len(lines)+1), Box: w.Box})
				idx = len(lines) - 1
			}
			lines[idx].Box = lines[idx].Box.Union(w.Box)
			lines[idx].Words = append(lines[idx].Words, w)
		}
	} else {
		for _, w := range words {
			idx := -1
			overlap, distance := -1.0, math.Inf(1)
			for i, l := range lines {
				ov := float64(max(0, min(w.Box.Y2, l.Box.Y2)-max(w.Box.Y1, l.Box.Y1))) / float64(max(1, w.Box.Y2-w.Box.Y1))
				dist := math.Abs(float64(l.Box.Y1 + l.Box.Y2 - w.Box.Y1 - w.Box.Y2))
				if ov > overlap || (ov == overlap && dist < distance) {
					idx = i
					overlap, distance = ov, dist
				}
			}
			if idx >= 0 {
				lines[idx].Words = append(lines[idx].Words, w)
			}
		}
	}
	for i := range lines {
		slices.SortStableFunc(lines[i].Words, func(a, b Word) int {
			if a.Box.X1 != b.Box.X1 {
				return a.Box.X1 - b.Box.X1
			}
			return a.Box.Y1 - b.Box.Y1
		})
		if o.InferWords && len(lines[i].Words) == 0 {
			lines[i].Words = inferLineWords(lines[i])
		}
	}
	if best == 2 {
		p.Regions = nil
	}
	implicit := -1
	for _, ln := range lines {
		idx := -1
		area := 0.0
		for i, r := range p.Regions {
			a := r.Box.Intersect(ln.Box).Area()
			if a > area {
				area = a
				idx = i
			}
		}
		if idx < 0 {
			if implicit < 0 {
				implicit = len(p.Regions)
				p.Regions = append(p.Regions, Region{Type: RegionText, Implicit: true})
			}
			idx = implicit
		}
		p.Regions[idx].Lines = append(p.Regions[idx].Lines, ln)
		if p.Regions[idx].Implicit {
			p.Regions[idx].Box = p.Regions[idx].Box.Union(ln.Box)
		}
	}
	out.Page = p
	if err := Validate(p, l); err != nil {
		return out, err
	}
	return out, nil
}
func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// IIIFWriteOptions controls annotation identities and included granularities.
type IIIFWriteOptions struct {
	CanvasID string
	ID       func(Level, string) string
	Levels   []Level
}

// WriteIIIF writes ordered AnnotationPage items with fragment and optional SVG selectors.
func WriteIIIF(p Page, o IIIFWriteOptions) ([]map[string]any, error) {
	if err := Validate(p, Limits{}); err != nil {
		return nil, err
	}
	levels := o.Levels
	if len(levels) == 0 {
		levels = []Level{LevelLine, LevelWord}
	}
	var out []map[string]any
	for _, level := range levels {
		if err := CheckLevel(level); err != nil {
			return nil, err
		}
		for i, e := range p.Elements(level) {
			id := e.ID
			if id == "" {
				id = fmt.Sprintf("%s_%d", level, i+1)
			}
			if o.ID != nil {
				id = o.ID(level, id)
			}
			b := e.Box
			selector := map[string]any{"type": "FragmentSelector", "conformsTo": "http://www.w3.org/TR/media-frags/", "value": fmt.Sprintf("xywh=%d,%d,%d,%d", b.X1, b.Y1, b.X2-b.X1, b.Y2-b.Y1)}
			var sel any = selector
			if len(e.Polygon) > 0 || len(e.Baseline) > 0 {
				svg := `<svg xmlns="http://www.w3.org/2000/svg">`
				if len(e.Polygon) > 0 {
					svg += `<polygon points="` + pointString(e.Polygon) + `"/>`
				}
				if len(e.Baseline) > 0 {
					svg += `<polyline class="baseline" points="` + pointString(e.Baseline) + `"/>`
				}
				svg += `</svg>`
				sel = []any{selector, map[string]any{"type": "SvgSelector", "value": svg}}
			}
			body := []map[string]any{{"type": "TextualBody", "purpose": "transcribing", "value": e.Text}}
			g := string(level)
			if level == LevelRegion {
				g = "block"
				body = append(body, map[string]any{"type": "TextualBody", "purpose": "classifying", "value": string(e.Type)})
			}
			out = append(out, map[string]any{"id": id, "type": "Annotation", "motivation": "supplementing", "textGranularity": g, "body": body, "target": map[string]any{"type": "SpecificResource", "source": o.CanvasID, "selector": sel}})
		}
	}
	return out, nil
}

// inferLineWords is restricted to legacy text exports, never the normal reader.
func inferLineWords(line Line) []Word {
	tokens := strings.Fields(line.Text)
	if len(tokens) == 0 {
		return nil
	}
	box := line.Box
	if box.X2-box.X1 < len(tokens) {
		return []Word{{ID: line.ID + "_word_1", Text: strings.Join(tokens, " "), Box: box}}
	}
	step := max(1, (box.X2-box.X1)/len(tokens))
	words := make([]Word, len(tokens))
	for i, text := range tokens {
		x1 := box.X1 + i*step
		x2 := x1 + step
		if i == len(tokens)-1 {
			x2 = box.X2
		}
		words[i] = Word{ID: fmt.Sprintf("%s_word_%d", line.ID, i+1), Text: text, Box: Rect{x1, box.Y1, x2, box.Y2}}
	}
	return words
}
