package ocrpage

import (
	"fmt"
	"html"
	"strings"
)

func escape(s string) string {
	return html.EscapeString(strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r', r >= 0x20 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD, r >= 0x10000 && r <= 0x10FFFF:
			return r
		}
		return '\uFFFD'
	}, strings.TrimSpace(s)))
}
func pointString(p []Point) string {
	v := make([]string, len(p))
	for i, p := range p {
		v[i] = fmt.Sprintf("%d,%d", p.X, p.Y)
	}
	return strings.Join(v, " ")
}
func polygon(b Rect, p []Point) []Point {
	if len(p) > 0 {
		return p
	}
	return b.Polygon()
}
func identifier(id, prefix string, i int) string {
	if id != "" {
		return escape(id)
	}
	return fmt.Sprintf("%s%d", prefix, i+1)
}

// PageXMLOptions sets deterministic PAGE metadata.
type PageXMLOptions struct{ Creator, Created, LastChange, ImageFilename string }

// WritePageXML writes PAGE 2019 geometry, baselines, types and reading order.
func WritePageXML(p Page, o PageXMLOptions) ([]byte, error) {
	if err := Validate(p, Limits{}); err != nil {
		return nil, err
	}
	if o.Creator == "" {
		o.Creator = "Scribe"
	}
	if o.Created == "" {
		o.Created = "1970-01-01T00:00:00Z"
	}
	if o.LastChange == "" {
		o.LastChange = o.Created
	}
	if o.ImageFilename == "" {
		o.ImageFilename = "source-image.png"
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<PcGts xmlns=\"http://schema.primaresearch.org/PAGE/gts/pagecontent/2019-07-15\">\n<Metadata>\n")
	fmt.Fprintf(&b, "<Creator>%s</Creator>\n<Created>%s</Created>\n<LastChange>%s</LastChange>\n</Metadata>\n", escape(o.Creator), escape(o.Created), escape(o.LastChange))
	fmt.Fprintf(&b, "<Page imageFilename=\"%s\" imageWidth=\"%d\" imageHeight=\"%d\">\n", escape(o.ImageFilename), p.Width, p.Height)
	simple := len(p.Regions) == 1 && p.Regions[0].Implicit
	ids := make([]string, len(p.Regions))
	for i, r := range p.Regions {
		ids[i] = identifier(r.ID, "r", i)
		if simple {
			ids[i] = "r1"
		}
	}
	if !simple && len(ids) > 0 {
		b.WriteString("<ReadingOrder><OrderedGroup id=\"order\">")
		for i, id := range ids {
			fmt.Fprintf(&b, `<RegionRefIndexed index="%d" regionRef="%s"/>`, i, id)
		}
		b.WriteString("</OrderedGroup></ReadingOrder>\n")
	}
	li := 0
	for ri, r := range p.Regions {
		tag := "TextRegion"
		switch r.Type {
		case RegionTable:
			tag = "TableRegion"
		case RegionImage:
			tag = "ImageRegion"
		case RegionSeparator:
			tag = "SeparatorRegion"
		case RegionNoise:
			tag = "NoiseRegion"
		case RegionMath:
			tag = "MathsRegion"
		}
		fmt.Fprintf(&b, `<%s id="%s"`, tag, ids[ri])
		if tag == "TextRegion" && !simple {
			fmt.Fprintf(&b, ` type="%s"`, escape(string(r.Type)))
		}
		b.WriteString(">\n")
		rb, rp := r.Box, r.Polygon
		if simple {
			rb = Rect{0, 0, p.Width, p.Height}
			rp = nil
		}
		fmt.Fprintf(&b, "<Coords points=\"%s\"/>\n", pointString(polygon(rb, rp)))
		writeLine := func(l Line) {
			li++
			id := identifier(l.ID, "l", li-1)
			if simple {
				id = fmt.Sprintf("l%d", li)
			}
			fmt.Fprintf(&b, `<TextLine id="%s"><Coords points="%s"/>`, id, pointString(polygon(l.Box, l.Polygon)))
			if len(l.Baseline) > 0 {
				fmt.Fprintf(&b, `<Baseline points="%s"/>`, pointString(l.Baseline))
			}
			for j, w := range l.Words {
				wid := identifier(w.ID, fmt.Sprintf("w%d_", li), j)
				if simple {
					wid = fmt.Sprintf("w%d_%d", li, j+1)
				}
				fmt.Fprintf(&b, `<Word id="%s"><Coords points="%s"/><TextEquiv><Unicode>%s</Unicode></TextEquiv></Word>`, wid, pointString(polygon(w.Box, w.Polygon)), escape(w.Text))
			}
			fmt.Fprintf(&b, "<TextEquiv><Unicode>%s</Unicode></TextEquiv></TextLine>\n", escape(l.PlainText()))
		}
		if r.Table != nil {
			used := map[string]bool{}
			for ci, c := range r.Table.Cells {
				fmt.Fprintf(&b, `<TableCell id="%s_cell_%d" row="%d" col="%d" rowSpan="%d" colSpan="%d"><Coords points="%s"/>`, ids[ri], ci+1, c.Row, c.Col, c.RowSpan, c.ColSpan, pointString(c.Box.Polygon()))
				for _, l := range r.Lines {
					for _, id := range c.LineIDs {
						if id == l.ID {
							writeLine(l)
							used[id] = true
							break
						}
					}
				}
				b.WriteString("</TableCell>\n")
			}
			for _, l := range r.Lines {
				if !used[l.ID] {
					writeLine(l)
				}
			}
		} else {
			for _, l := range r.Lines {
				writeLine(l)
			}
		}
		fmt.Fprintf(&b, "</%s>\n", tag)
	}
	b.WriteString("</Page>\n</PcGts>\n")
	return []byte(b.String()), nil
}

// HOCROptions controls hOCR producer metadata.
type HOCROptions struct{ OCRSystem string }

// WriteHOCR emits hOCR with word confidence and linear baselines.
func WriteHOCR(p Page, o HOCROptions) ([]byte, error) {
	if err := Validate(p, Limits{}); err != nil {
		return nil, err
	}
	if o.OCRSystem == "" {
		o.OCRSystem = "custom-word-detection-with-chatgpt"
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\"\n    \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd\">\n<html xmlns=\"http://www.w3.org/1999/xhtml\" xml:lang=\"en\" lang=\"en\">\n<head>\n<title></title>\n<meta http-equiv=\"Content-Type\" content=\"text/html; charset=utf-8\" />\n")
	fmt.Fprintf(&b, "<meta name='ocr-system' content='%s' />\n", escape(o.OCRSystem))
	b.WriteString("<meta name='ocr-capabilities' content='ocr_page ocr_carea ocr_par ocr_line ocrx_word' />\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<div class='ocr_page' id='page_1' title='bbox 0 0 %d %d'>\n", p.Width, p.Height)
	for ri, r := range p.Regions {
		if !r.Implicit {
			fmt.Fprintf(&b, "<div class='ocr_carea' id='%s' title='bbox %d %d %d %d'>\n", identifier(r.ID, "region_", ri), r.Box.X1, r.Box.Y1, r.Box.X2, r.Box.Y2)
		}
		for i, l := range r.Lines {
			base := ""
			if len(l.Baseline) >= 2 {
				a, z := l.Baseline[0], l.Baseline[len(l.Baseline)-1]
				if z.X != a.X {
					slope := float64(z.Y-a.Y) / float64(z.X-a.X)
					offset := float64(a.Y-l.Box.Y2) + slope*float64(l.Box.X1-a.X)
					base = fmt.Sprintf("; baseline %g %g", slope, offset)
				}
			}
			fmt.Fprintf(&b, "<span class='ocr_line' id='%s' title='bbox %d %d %d %d%s'>", identifier(l.ID, fmt.Sprintf("r%d_line_", ri), i), l.Box.X1, l.Box.Y1, l.Box.X2, l.Box.Y2, base)
			for j, w := range l.Words {
				fmt.Fprintf(&b, "<span class='ocrx_word' id='%s' title='bbox %d %d %d %d; x_wconf %.0f'>%s</span> ", identifier(w.ID, fmt.Sprintf("r%d_l%d_word_", ri, i), j), w.Box.X1, w.Box.Y1, w.Box.X2, w.Box.Y2, w.Confidence*100, escape(w.Text))
			}
			if len(l.Words) == 0 {
				b.WriteString(escape(l.Text))
			}
			b.WriteString("</span>\n")
		}
		if !r.Implicit {
			b.WriteString("</div>\n")
		}
	}
	b.WriteString("</div>\n</body>\n</html>\n")
	return []byte(b.String()), nil
}

// WriteALTO emits ALTO v4 with ordered blocks, word boxes and baselines.
func WriteALTO(p Page) ([]byte, error) {
	if err := Validate(p, Limits{}); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<alto xmlns=\"http://www.loc.gov/standards/alto/ns-v4#\" SCHEMAVERSION=\"4.4\">\n")
	simple := len(p.Regions) == 1 && p.Regions[0].Implicit
	if !simple {
		b.WriteString("<Tags>")
		seen := map[RegionType]bool{}
		for _, r := range p.Regions {
			if !seen[r.Type] {
				fmt.Fprintf(&b, `<LayoutTag ID="type-%s" LABEL="%s"/>`, escape(string(r.Type)), escape(string(r.Type)))
				seen[r.Type] = true
			}
		}
		b.WriteString("</Tags>\n")
	}
	b.WriteString("<Layout>\n")
	fmt.Fprintf(&b, "<Page ID=\"P1\" PHYSICAL_IMG_NR=\"1\" WIDTH=\"%d\" HEIGHT=\"%d\">\n<PrintSpace HPOS=\"0\" VPOS=\"0\" WIDTH=\"%d\" HEIGHT=\"%d\">\n", p.Width, p.Height, p.Width, p.Height)
	li := 0
	for i, r := range p.Regions {
		if simple {
			b.WriteString("<TextBlock ID=\"TB1\">\n")
		} else {
			fmt.Fprintf(&b, "<TextBlock ID=\"%s\" TAGREFS=\"type-%s\" HPOS=\"%d\" VPOS=\"%d\" WIDTH=\"%d\" HEIGHT=\"%d\">\n", identifier(r.ID, "TB", i), escape(string(r.Type)), r.Box.X1, r.Box.Y1, r.Box.X2-r.Box.X1, r.Box.Y2-r.Box.Y1)
		}
		if len(r.Polygon) > 0 {
			fmt.Fprintf(&b, `<Shape><Polygon POINTS="%s"/></Shape>`, pointString(r.Polygon))
		}
		for _, l := range r.Lines {
			li++
			fmt.Fprintf(&b, `<TextLine ID="TL%d" HPOS="%d" VPOS="%d" WIDTH="%d" HEIGHT="%d"`, li, l.Box.X1, l.Box.Y1, max(1, l.Box.X2-l.Box.X1), max(1, l.Box.Y2-l.Box.Y1))
			if len(l.Baseline) > 0 {
				fmt.Fprintf(&b, ` BASELINE="%s"`, pointString(l.Baseline))
			}
			b.WriteString(">")
			if len(l.Polygon) > 0 {
				fmt.Fprintf(&b, `<Shape><Polygon POINTS="%s"/></Shape>`, pointString(l.Polygon))
			}
			words := l.Words
			if len(words) == 0 && l.Text != "" {
				words = []Word{{Text: l.Text, Box: l.Box}}
			}
			for j, w := range words {
				fmt.Fprintf(&b, `<String ID="S%d_%d" CONTENT="%s" HPOS="%d" VPOS="%d" WIDTH="%d" HEIGHT="%d"`, li, j+1, escape(w.Text), w.Box.X1, w.Box.Y1, max(1, w.Box.X2-w.Box.X1), max(1, w.Box.Y2-w.Box.Y1))
				if w.Confidence > 0 {
					fmt.Fprintf(&b, ` WC="%g"`, w.Confidence)
				}
				if len(w.Polygon) > 0 {
					fmt.Fprintf(&b, `><Shape><Polygon POINTS="%s"/></Shape></String>`, pointString(w.Polygon))
				} else {
					b.WriteString("/>")
				}
			}
			b.WriteString("</TextLine>\n")
		}
		b.WriteString("</TextBlock>\n")
	}
	b.WriteString("</PrintSpace>\n</Page>\n</Layout>\n</alto>\n")
	return []byte(b.String()), nil
}

// WriteText emits plain text in reading order.
func WriteText(p Page) ([]byte, error) { return []byte(p.PlainText()), nil }
