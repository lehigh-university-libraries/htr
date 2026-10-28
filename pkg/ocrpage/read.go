package ocrpage

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ReadOptions controls format readers and supplies missing image dimensions.
type ReadOptions struct {
	Limits                    Limits
	CanvasWidth, CanvasHeight int
	DPI                       float64
}

// Parse detects and reads a supported OCR format.
func Parse(data []byte, o ReadOptions) (Page, error) {
	if len(data) > o.Limits.defaults().MaxBytes {
		return Page{}, ErrLimit
	}
	f := FormatText
	s := strings.TrimSpace(string(data))
	switch {
	case strings.HasPrefix(s, "{"):
		var h map[string]json.RawMessage
		if err := json.Unmarshal(data, &h); err != nil {
			return Page{}, fmt.Errorf("ocrpage: invalid JSON")
		}
		switch {
		case h["htrPage"] != nil:
			f = FormatHTRJSON
		case h["items"] != nil || h["@type"] != nil:
			f = FormatIIIF
		case h["lines"] != nil:
			f = FormatKraken
		default:
			return Page{}, errors.New("ocrpage: unrecognized JSON format")
		}
	case strings.HasPrefix(s, "<"):
		switch {
		case strings.Contains(s, "ocr_page"):
			f = FormatHOCR
		case strings.Contains(s, "PcGts"):
			f = FormatPageXML
		case strings.Contains(strings.ToLower(s), "alto"):
			f = FormatALTO
		default:
			return Page{}, errors.New("ocrpage: unrecognized XML format")
		}
	}
	return ParseFormat(data, f, o)
}

// ParseFormat reads a known format and validates its result.
func ParseFormat(data []byte, f Format, o ReadOptions) (Page, error) {
	l := o.Limits.defaults()
	if len(data) > l.MaxBytes {
		return Page{}, ErrLimit
	}
	var p Page
	var err error
	switch f {
	case FormatHTRJSON:
		if err = checkJSON(data, l); err == nil {
			var v struct {
				Version int `json:"htrPage"`
				Page
			}
			err = json.Unmarshal(data, &v)
			p = v.Page
			if err == nil && v.Version != 1 {
				err = errors.New("ocrpage: unsupported htr page version")
			}
		}
	case FormatKraken:
		p, err = readKraken(data, o)
	case FormatIIIF:
		var v IIIFReadResult
		v, err = ReadIIIF(data, IIIFReadOptions{CanvasWidth: o.CanvasWidth, CanvasHeight: o.CanvasHeight, Limits: o.Limits})
		p = v.Page
	case FormatHOCR, FormatPageXML, FormatALTO:
		var root *node
		root, err = readXML(data, f == FormatHOCR, l)
		if err == nil {
			switch f {
			case FormatHOCR:
				p, err = readHOCR(root, o)
			case FormatPageXML:
				p, err = readPAGE(root, o)
			case FormatALTO:
				p, err = readALTO(root, o)
			}
		}
	case FormatText:
		for _, s := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if len(p.Regions) == 0 {
				p = FromLines(o.CanvasWidth, o.CanvasHeight, nil)
			}
			p.Regions[0].Lines = append(p.Regions[0].Lines, Line{Text: s})
		}
	default:
		err = fmt.Errorf("ocrpage: unsupported format %q", f)
	}
	if err != nil {
		return Page{}, err
	}
	if f != FormatHTRJSON {
		p.Source = f
	}
	if p.Width == 0 {
		p.Width = o.CanvasWidth
	}
	if p.Height == 0 {
		p.Height = o.CanvasHeight
	}
	if err = Validate(p, l); err != nil {
		return Page{}, err
	}
	return p, nil
}
func checkJSON(data []byte, l Limits) error {
	d := json.NewDecoder(bytes.NewReader(data))
	depth, n := 0, 0
	for {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("ocrpage: invalid JSON")
		}
		n++
		if n > l.MaxElements*16 {
			return ErrLimit
		}
		if c, ok := t.(json.Delim); ok {
			if c == '{' || c == '[' {
				depth++
				if depth > l.MaxDepth {
					return ErrLimit
				}
			} else {
				depth--
			}
		}
	}
	return nil
}

// WriteJSON serializes the lossless versioned htr page wire format.
func WriteJSON(p Page) ([]byte, error) {
	if err := Validate(p, Limits{}); err != nil {
		return nil, err
	}
	return json.MarshalIndent(struct {
		Version int `json:"htrPage"`
		Page
	}{1, p}, "", "  ")
}

type node struct {
	name     string
	a        map[string]string
	children []*node
	parts    []any
}

func readXML(data []byte, html bool, l Limits) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	if html {
		d.Strict = false
		d.AutoClose = xml.HTMLAutoClose
		d.Entity = xml.HTMLEntity
	}
	r := &node{}
	stack := []*node{r}
	n := 0
	for {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("ocrpage: invalid XML")
		}
		switch v := t.(type) {
		case xml.StartElement:
			n++
			if n > l.MaxElements || len(stack) > l.MaxDepth {
				return nil, ErrLimit
			}
			c := &node{name: v.Name.Local, a: map[string]string{}}
			for _, a := range v.Attr {
				c.a[a.Name.Local] = a.Value
			}
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, c)
			parent.parts = append(parent.parts, c)
			stack = append(stack, c)
		case xml.EndElement:
			if len(stack) < 2 {
				return nil, errors.New("ocrpage: unbalanced XML")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			current := stack[len(stack)-1]
			current.parts = append(current.parts, string(v))
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("ocrpage: unclosed XML")
	}
	return r, nil
}
func (n *node) all(name string) []*node {
	var v []*node
	for _, c := range n.children {
		if c.name == name {
			v = append(v, c)
		}
		v = append(v, c.all(name)...)
	}
	return v
}
func (n *node) first(name string) *node {
	v := n.all(name)
	if len(v) > 0 {
		return v[0]
	}
	return &node{a: map[string]string{}}
}
func (n *node) content() string {
	var b strings.Builder
	var write func(*node)
	write = func(n *node) {
		for _, part := range n.parts {
			switch v := part.(type) {
			case string:
				b.WriteString(v)
			case *node:
				write(v)
			}
		}
	}
	write(n)
	return strings.TrimSpace(b.String())
}
func (n *node) class(c ...string) bool {
	for _, s := range strings.Fields(n.a["class"]) {
		if slices.Contains(c, s) {
			return true
		}
	}
	return false
}
func numbers(s string) ([]float64, error) {
	s = strings.NewReplacer(",", " ", ";", " ").Replace(s)
	var v []float64
	for _, t := range strings.Fields(s) {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e9 {
			return nil, errors.New("ocrpage: invalid coordinate")
		}
		v = append(v, f)
	}
	return v, nil
}
func points(s string) ([]Point, error) {
	v, err := numbers(s)
	if err != nil {
		return nil, err
	}
	if len(v)%2 != 0 {
		return nil, errors.New("ocrpage: incomplete point")
	}
	var p []Point
	for i := 0; i < len(v); i += 2 {
		p = append(p, Point{int(math.Round(v[i])), int(math.Round(v[i+1]))})
	}
	return p, nil
}
func integer(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	v, e := strconv.Atoi(s)
	if e != nil || v < 0 || v > 1e9 {
		return 0, errors.New("ocrpage: invalid integer")
	}
	return v, nil
}
func confidence(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("ocrpage: invalid confidence")
	}
	return v, nil
}
func coords(n *node) (Rect, []Point, error) {
	p, err := points(n.first("Coords").a["points"])
	return BoundingBox(p), p, err
}
func unicodeText(n *node) string {
	for _, c := range n.children {
		if c.name == "TextEquiv" {
			return c.first("Unicode").content()
		}
	}
	return ""
}
func readPAGE(root *node, o ReadOptions) (Page, error) {
	pn := root.first("Page")
	w, err := integer(pn.a["imageWidth"])
	if err != nil {
		return Page{}, err
	}
	h, err := integer(pn.a["imageHeight"])
	if err != nil {
		return Page{}, err
	}
	p := Page{Width: w, Height: h}
	parseLine := func(n *node) (Line, error) {
		b, poly, e := coords(n)
		if e != nil {
			return Line{}, e
		}
		base, e := points(n.first("Baseline").a["points"])
		if e != nil {
			return Line{}, e
		}
		l := Line{ID: n.a["id"], Box: b, Polygon: poly, Baseline: base, Text: unicodeText(n)}
		for _, wn := range n.all("Word") {
			b, poly, e := coords(wn)
			if e != nil {
				return Line{}, e
			}
			l.Words = append(l.Words, Word{ID: wn.a["id"], Text: unicodeText(wn), Box: b, Polygon: poly})
		}
		return l, nil
	}
	for _, n := range pn.children {
		if !strings.HasSuffix(n.name, "Region") {
			continue
		}
		b, poly, e := coords(n)
		if e != nil {
			return Page{}, e
		}
		label := n.a["type"]
		if label == "" {
			label = strings.ToLower(strings.TrimSuffix(n.name, "Region"))
		}
		r := Region{ID: n.a["id"], Type: CanonicalType(label), SourceType: label, Box: b, Polygon: poly}
		for _, ln := range n.all("TextLine") {
			l, e := parseLine(ln)
			if e != nil {
				return Page{}, e
			}
			r.Lines = append(r.Lines, l)
		}
		if n.name == "TableRegion" {
			r.Table = &Table{}
			for _, cn := range n.all("TableCell") {
				row, e := integer(cn.a["row"])
				if e != nil {
					return Page{}, e
				}
				col, e := integer(cn.a["col"])
				if e != nil {
					return Page{}, e
				}
				rs, e := integer(cn.a["rowSpan"])
				if e != nil {
					return Page{}, e
				}
				cs, e := integer(cn.a["colSpan"])
				if e != nil {
					return Page{}, e
				}
				cb, _, e := coords(cn)
				if e != nil {
					return Page{}, e
				}
				c := Cell{Row: row, Col: col, RowSpan: max(1, rs), ColSpan: max(1, cs), Box: cb}
				for _, ln := range cn.all("TextLine") {
					c.LineIDs = append(c.LineIDs, ln.a["id"])
				}
				r.Table.Rows = max(r.Table.Rows, row+c.RowSpan)
				r.Table.Cols = max(r.Table.Cols, col+c.ColSpan)
				r.Table.Cells = append(r.Table.Cells, c)
			}
		}
		p.Regions = append(p.Regions, r)
	}
	refs := pn.first("ReadingOrder").all("RegionRefIndexed")
	slices.SortStableFunc(refs, func(a, b *node) int {
		x, _ := strconv.Atoi(a.a["index"])
		y, _ := strconv.Atoi(b.a["index"])
		return x - y
	})
	rank := map[string]int{}
	for i, r := range refs {
		rank[r.a["regionRef"]] = i
	}
	if len(rank) > 0 {
		slices.SortStableFunc(p.Regions, func(a, b Region) int {
			x, ok := rank[a.ID]
			if !ok {
				x = len(rank)
			}
			y, ok := rank[b.ID]
			if !ok {
				y = len(rank)
			}
			return x - y
		})
	}
	return p, nil
}
func titleProps(s string) map[string]string {
	m := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		kv := strings.Fields(part)
		if len(kv) > 1 {
			m[kv[0]] = strings.Join(kv[1:], " ")
		}
	}
	return m
}
func hocrBox(n *node) (Rect, error) {
	v, e := numbers(titleProps(n.a["title"])["bbox"])
	if e != nil {
		return Rect{}, e
	}
	if len(v) != 4 {
		return Rect{}, errors.New("ocrpage: missing hOCR bbox")
	}
	return Rect{int(math.Round(v[0])), int(math.Round(v[1])), int(math.Round(v[2])), int(math.Round(v[3]))}, nil
}
func readHOCR(root *node, o ReadOptions) (Page, error) {
	var pages []*node
	var walk func(*node)
	walk = func(n *node) {
		if n.class("ocr_page") {
			pages = append(pages, n)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	if len(pages) != 1 {
		return Page{}, errors.New("ocrpage: expected one hOCR page")
	}
	pn := pages[0]
	b, err := hocrBox(pn)
	if err != nil {
		return Page{}, err
	}
	p := Page{Width: b.X2, Height: b.Y2}
	current, implicit := -1, -1
	var visit func(*node) error
	visit = func(n *node) error {
		if n.class("ocr_carea", "ocr_par") && (current < 0 || p.Regions[current].Implicit) {
			r := Region{ID: n.a["id"], Type: RegionText}
			var e error
			r.Box, e = hocrBox(n)
			if e != nil {
				return e
			}
			p.Regions = append(p.Regions, r)
			prior := current
			current = len(p.Regions) - 1
			for _, c := range n.children {
				if e = visit(c); e != nil {
					return e
				}
			}
			current = prior
			return nil
		}
		if n.class("ocr_line", "ocrx_line") {
			b, e := hocrBox(n)
			if e != nil {
				return e
			}
			l := Line{ID: n.a["id"], Box: b, Text: n.content()}
			props := titleProps(n.a["title"])
			if props["baseline"] != "" {
				v, e := numbers(props["baseline"])
				if e != nil || len(v) != 2 {
					return errors.New("ocrpage: invalid hOCR baseline")
				}
				l.Baseline = []Point{{b.X1, b.Y2 + int(math.Round(v[1]))}, {b.X2, b.Y2 + int(math.Round(v[1]+v[0]*float64(b.X2-b.X1)))}}
			}
			var words func(*node) error
			words = func(n *node) error {
				if n.class("ocrx_word") {
					b, e := hocrBox(n)
					if e != nil {
						return e
					}
					c, e := confidence(titleProps(n.a["title"])["x_wconf"])
					if e != nil {
						return e
					}
					l.Words = append(l.Words, Word{ID: n.a["id"], Box: b, Text: n.content(), Confidence: c / 100})
					return nil
				}
				for _, c := range n.children {
					if e := words(c); e != nil {
						return e
					}
				}
				return nil
			}
			if e = words(n); e != nil {
				return e
			}
			if current < 0 {
				if implicit < 0 {
					implicit = len(p.Regions)
					p.Regions = append(p.Regions, Region{Type: RegionText, Implicit: true})
				}
				current = implicit
			}
			p.Regions[current].Lines = append(p.Regions[current].Lines, l)
			if p.Regions[current].Implicit {
				p.Regions[current].Box = p.Regions[current].Box.Union(b)
			}
			return nil
		}
		for _, c := range n.children {
			if e := visit(c); e != nil {
				return e
			}
		}
		return nil
	}
	if err = visit(pn); err != nil {
		return Page{}, err
	}
	return p, nil
}
func readALTO(root *node, o ReadOptions) (Page, error) {
	pn := root.first("Page")
	w, e := integer(pn.a["WIDTH"])
	if e != nil {
		return Page{}, e
	}
	h, e := integer(pn.a["HEIGHT"])
	if e != nil {
		return Page{}, e
	}
	unit := root.first("MeasurementUnit").content()
	scale := 1.0
	switch unit {
	case "", "pixel":
	case "mm10", "inch1200":
		if o.DPI <= 0 || math.IsNaN(o.DPI) || math.IsInf(o.DPI, 0) {
			return Page{}, errors.New("ocrpage: ALTO physical units require DPI")
		}
		if unit == "mm10" {
			scale = o.DPI / 254
		} else {
			scale = o.DPI / 1200
		}
	default:
		return Page{}, errors.New("ocrpage: unsupported ALTO unit")
	}
	box := func(n *node) (Rect, error) {
		var v [4]int
		for i, k := range []string{"HPOS", "VPOS", "WIDTH", "HEIGHT"} {
			a, err := numbers(n.a[k])
			if err != nil || len(a) != 1 {
				return Rect{}, errors.New("ocrpage: invalid ALTO geometry")
			}
			v[i] = int(math.Round(a[0]))
		}
		return Rect{v[0], v[1], v[0] + v[2], v[1] + v[3]}, nil
	}
	p := Page{Width: w, Height: h}
	tags := map[string]string{}
	for _, tag := range root.first("Tags").children {
		tags[tag.a["ID"]] = tag.a["LABEL"]
	}
	shape := func(n *node) ([]Point, error) {
		for _, c := range n.children {
			if strings.EqualFold(c.name, "Shape") {
				return points(c.first("Polygon").a["POINTS"])
			}
		}
		return nil, nil
	}
	for _, bn := range pn.all("TextBlock") {
		var b Rect
		if bn.a["WIDTH"] != "" {
			b, e = box(bn)
			if e != nil {
				return Page{}, e
			}
		}
		label := bn.a["TYPE"]
		if label == "" {
			for _, ref := range strings.Fields(bn.a["TAGREFS"]) {
				if tag := tags[ref]; tag != "" {
					label = tag
					break
				}
			}
		}
		if label == "" {
			label = "text"
		}
		r := Region{ID: bn.a["ID"], Type: CanonicalType(label), SourceType: label, Box: b}
		r.Polygon, e = shape(bn)
		if e != nil {
			return Page{}, e
		}
		for _, ln := range bn.all("TextLine") {
			b, e := box(ln)
			if e != nil {
				return Page{}, e
			}
			l := Line{ID: ln.a["ID"], Box: b}
			l.Polygon, e = shape(ln)
			if e != nil {
				return Page{}, e
			}
			if s := ln.a["BASELINE"]; s != "" {
				v, e := numbers(s)
				if e != nil {
					return Page{}, e
				}
				if len(v) == 1 {
					y := int(math.Round(v[0]))
					l.Baseline = []Point{{b.X1, y}, {b.X2, y}}
				} else {
					l.Baseline, e = points(s)
					if e != nil {
						return Page{}, e
					}
				}
			}
			for _, wn := range ln.all("String") {
				b, e := box(wn)
				if e != nil {
					return Page{}, e
				}
				c, e := confidence(wn.a["WC"])
				if e != nil {
					return Page{}, e
				}
				poly, e := shape(wn)
				if e != nil {
					return Page{}, e
				}
				l.Words = append(l.Words, Word{ID: wn.a["ID"], Text: wn.a["CONTENT"], Box: b, Polygon: poly, Confidence: c})
			}
			r.Lines = append(r.Lines, l)
			if bn.a["WIDTH"] == "" {
				r.Box = r.Box.Union(l.Box)
			}
		}
		p.Regions = append(p.Regions, r)
	}
	if scale != 1 {
		p = p.Scale(int(math.Round(float64(w)*scale)), int(math.Round(float64(h)*scale)))
	}
	return p, nil
}
