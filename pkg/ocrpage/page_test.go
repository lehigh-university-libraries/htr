package ocrpage

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func samplePage() Page {
	return FromLines(200, 100, []Line{{ID: "line_1", Box: Rect{10, 10, 110, 30}, Baseline: []Point{{10, 25}, {110, 25}}, Words: []Word{{ID: "word_1", Text: "été & <水>", Box: Rect{10, 10, 110, 30}, Confidence: .95}}}, {ID: "line_2", Box: Rect{10, 40, 100, 60}, Text: "second line"}})
}
func TestRoundTrips(t *testing.T) {
	p := samplePage()
	for _, f := range []Format{FormatHTRJSON, FormatHOCR, FormatPageXML, FormatALTO, FormatIIIF} {
		t.Run(string(f), func(t *testing.T) {
			var data []byte
			var err error
			switch f {
			case FormatHTRJSON:
				data, err = WriteJSON(p)
			case FormatHOCR:
				data, err = WriteHOCR(p, HOCROptions{})
			case FormatPageXML:
				data, err = WritePageXML(p, PageXMLOptions{})
			case FormatALTO:
				data, err = WriteALTO(p)
			case FormatIIIF:
				var items []map[string]any
				items, err = WriteIIIF(p, IIIFWriteOptions{})
				if err == nil {
					data, err = json.Marshal(map[string]any{"type": "AnnotationPage", "items": items, "width": p.Width, "height": p.Height})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := Parse(data, ReadOptions{})
			if err != nil {
				t.Fatalf("%v\n%s", err, data)
			}
			if got.Width != p.Width || got.Height != p.Height || got.PlainText() != p.PlainText() {
				t.Fatalf("got %#v text %q; want %q", got, got.PlainText(), p.PlainText())
			}
			if len(got.Lines()) != 2 || got.Lines()[0].Box != p.Lines()[0].Box {
				t.Fatalf("lost line geometry: %+v", got.Lines())
			}
			if f != FormatALTO && len(got.Lines()[0].Baseline) != 2 {
				t.Fatal("lost baseline")
			}
			if f == FormatHTRJSON {
				if !reflect.DeepEqual(got, p) {
					t.Fatalf("JSON not lossless\n%+v\n%+v", got, p)
				}
			}
		})
	}
}
func TestReaders(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		options    ReadOptions
		want       string
		bad        bool
	}{
		{"page2013", `<PcGts xmlns="http://schema.primaresearch.org/PAGE/gts/pagecontent/2013-07-15"><Page imageWidth="100" imageHeight="100"><ReadingOrder><OrderedGroup><RegionRefIndexed index="0" regionRef="b"/><RegionRefIndexed index="1" regionRef="a"/></OrderedGroup></ReadingOrder><TextRegion id="a"><Coords points="0,0 50,0 50,20 0,20"/><TextLine><Coords points="0,0 50,0 50,20 0,20"/><TextEquiv><Unicode>A</Unicode></TextEquiv></TextLine></TextRegion><TextRegion id="b"><Coords points="0,30 50,30 50,50 0,50"/><TextLine><Coords points="0,30 50,30 50,50 0,50"/><TextEquiv><Unicode>B</Unicode></TextEquiv></TextLine></TextRegion></Page></PcGts>`, ReadOptions{}, "B\nA", false},
		{"alto2", `<alto xmlns="http://www.loc.gov/standards/alto/ns-v2#"><Description><MeasurementUnit>mm10</MeasurementUnit></Description><Layout><Page WIDTH="100" HEIGHT="100"><TextBlock HPOS="0" VPOS="0" WIDTH="100" HEIGHT="20"><TextLine HPOS="0" VPOS="0" WIDTH="100" HEIGHT="20" BASELINE="15"><String CONTENT="A" HPOS="0" VPOS="0" WIDTH="20" HEIGHT="20"/></TextLine></TextBlock></Page></Layout></alto>`, ReadOptions{DPI: 254}, "A", false},
		{"physicalNoDPI", `<alto><MeasurementUnit>mm10</MeasurementUnit><Page WIDTH="100" HEIGHT="100"/></alto>`, ReadOptions{}, "", true},
		{"kraken7", `{"lines":[{"id":"l","boundary":[[0,0],[80,0],[80,20],[0,20]],"baseline":[[0,15],[80,15]],"tags":{"type":"default"},"text":"abc"}]}`, ReadOptions{CanvasWidth: 100, CanvasHeight: 100}, "abc", false},
		{"krakenFallback", `{"lines":[{"baseline":[[0,15],[80,15]]}]}`, ReadOptions{CanvasWidth: 100, CanvasHeight: 100}, "", false},
		{"badPoint", `{"lines":[{"baseline":[[0],[80,15]]}]}`, ReadOptions{CanvasWidth: 100, CanvasHeight: 100}, "", true},
		{"outside", `{"htrPage":1,"width":10,"height":10,"regions":[{"box":{"x1":0,"y1":0,"x2":11,"y2":1}}]}`, ReadOptions{}, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.data), tt.options)
			if (err != nil) != tt.bad {
				t.Fatalf("error %v", err)
			}
			if err == nil && got.PlainText() != tt.want {
				t.Fatalf("text %q", got.PlainText())
			}
		})
	}
}
func TestLimitsAndValidation(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		lim        Limits
	}{
		{"bytes", "abcdef", Limits{MaxBytes: 5}},
		{"xmlDepth", `<PcGts><Page><a><b/></a></Page></PcGts>`, Limits{MaxDepth: 2}},
		{"jsonDepth", `{"htrPage":1,"regions":[[[[]]]]}`, Limits{MaxDepth: 3}},
		{"elements", `<PcGts><Page/><Page/></PcGts>`, Limits{MaxElements: 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data), ReadOptions{Limits: tt.lim})
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("wanted ErrLimit, got %v", err)
			}
		})
	}
	p := samplePage()
	p.Regions[0].Confidence = math.NaN()
	if Validate(p, Limits{}) == nil {
		t.Fatal("NaN accepted")
	}
}
func TestScaleDoesNotMutate(t *testing.T) {
	p := samplePage()
	before, _ := json.Marshal(p)
	q := p.Scale(400, 200)
	if q.Lines()[0].Box != (Rect{20, 20, 220, 60}) {
		t.Fatal(q)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("Scale mutated source")
	}
}
func TestIIIFSelectors(t *testing.T) {
	p := samplePage()
	p.Regions[0].Lines[0].Polygon = p.Lines()[0].Box.Polygon()
	items, e := WriteIIIF(p, IIIFWriteOptions{CanvasID: "canvas"})
	if e != nil {
		t.Fatal(e)
	}
	items = append(items, map[string]any{"body": "a comment", "target": "canvas#xywh=0,0,10,10"})
	data, _ := json.Marshal(map[string]any{"items": items})
	v, e := ReadIIIF(data, IIIFReadOptions{CanvasWidth: 200, CanvasHeight: 100})
	if e != nil {
		t.Fatal(e)
	}
	if v.Ignored != 1 || len(v.Page.Lines()[0].Polygon) != 4 || len(v.Page.Lines()[0].Baseline) != 2 {
		t.Fatalf("%+v", v)
	}
	for _, s := range []string{"xywh=percent:0,0,10,10", "xywh=-1,0,10,10", "xywh=0,0,NaN,10", "xywh=0,0,0,10"} {
		if _, e := ParseXYWH(s); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	r, e := ParseXYWH("canvas#xywh=pixel:1.4,2.6,10,20")
	if e != nil || r != (Rect{1, 3, 11, 23}) {
		t.Fatal(r, e)
	}
}
func TestHTRJSONRejectsTrailingData(t *testing.T) {
	_, err := ParseFormat([]byte(`{"htrPage":1} {}`), FormatHTRJSON, ReadOptions{})
	if err == nil {
		t.Fatal("accepted multiple JSON documents")
	}
}
func FuzzParse(f *testing.F) {
	for _, seed := range []string{`<PcGts><Page imageWidth="100" imageHeight="100"/></PcGts>`, `{"htrPage":1,"width":100,"height":100,"regions":[]}`, `{"lines":[]}`, `{"items":[]}`, strings.Repeat("x", 30)} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Parse(b, ReadOptions{Limits: Limits{MaxBytes: 65536, MaxElements: 1000, MaxDepth: 32}})
	})
}
func TestScribeWriterGoldens(t *testing.T) {
	root := "../../fixtures/segeval/scribe/"
	input, e := os.ReadFile(root + "expected_hocr.html")
	if e != nil {
		t.Fatal(e)
	}
	p, e := Parse(input, ReadOptions{})
	if e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct {
		name  string
		write func() ([]byte, error)
	}{
		{"expected_hocr.html", func() ([]byte, error) { return WriteHOCR(p, HOCROptions{}) }},
		{"expected_page.xml", func() ([]byte, error) { return WritePageXML(p, PageXMLOptions{}) }},
		{"expected_alto.xml", func() ([]byte, error) { return WriteALTO(p) }},
		{"expected_plain.txt", func() ([]byte, error) { return WriteText(p) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want, e := os.ReadFile(root + tt.name)
			if e != nil {
				t.Fatal(e)
			}
			got, e := tt.write()
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Scribe parity mismatch\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}
func TestRichALTOAndPageFallback(t *testing.T) {
	p := samplePage()
	p.Regions[0].Implicit = false
	p.Regions[0].Type = RegionHeading
	p.Regions[0].Polygon = p.Regions[0].Box.Polygon()
	p.Regions[0].Lines[0].Polygon = p.Lines()[0].Box.Polygon()
	p.Regions[0].Lines[0].Words[0].Polygon = p.Words()[0].Box.Polygon()
	data, e := WriteALTO(p)
	if e != nil {
		t.Fatal(e)
	}
	q, e := Parse(data, ReadOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if q.Regions[0].Type != RegionHeading || len(q.Regions[0].Polygon) != 4 || len(q.Lines()[0].Polygon) != 4 || len(q.Words()[0].Polygon) != 4 || q.Words()[0].Confidence != .95 {
		t.Fatal(q)
	}
	result, e := ReadIIIF([]byte(`{"items":[{"textGranularity":"page","body":"Whole page","target":"canvas"}]}`), IIIFReadOptions{CanvasWidth: 100, CanvasHeight: 200})
	if e != nil || result.Page.PlainText() != "Whole page" || result.Page.Lines()[0].Box != (Rect{X2: 100, Y2: 200}) {
		t.Fatal(result, e)
	}
}
