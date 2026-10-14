package layout

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

const pageXMLWords = `<?xml version="1.0" encoding="UTF-8"?>
<PcGts xmlns="http://schema.primaresearch.org/PAGE/gts/pagecontent/2019-07-15">
  <Page imageFilename="page.jpg" imageWidth="1000" imageHeight="800">
    <TextRegion id="r1">
      <Coords points="0,0 1000,0 1000,200 0,200"/>
      <TextLine id="l1">
        <Coords points="10,10 500,10 500,60 10,60"/>
        <Baseline points="10,55 500,55"/>
        <Word id="w1"><Coords points="10,10 200,10 200,60 10,60"/></Word>
        <Word id="w2"><Coords points="210,10 500,10 500,60 210,60"/></Word>
      </TextLine>
      <TextLine id="l2">
        <Coords points="10,70 500,70 500,120 10,120"/>
        <Word id="w3"><Coords points="10,70 500,70 500,120 10,120"/></Word>
      </TextLine>
    </TextRegion>
  </Page>
</PcGts>`

const pageXMLWordsResponse = "```xml\n" + `<?xml version="1.0" encoding="UTF-8"?>
<PcGts xmlns="http://schema.primaresearch.org/PAGE/gts/pagecontent/2019-07-15">
  <Page imageFilename="page.jpg" imageWidth="1000" imageHeight="800">
    <TextRegion id="r1">
      <Coords points="0,0 1000,0 1000,200 0,200"/>
      <TextLine id="l1">
        <Coords points="10,10 500,10 500,60 10,60"/>
        <Word id="w1"><Coords points="10,10 200,10 200,60 10,60"/><TextEquiv><Unicode>Fish &amp; Co</Unicode></TextEquiv></Word>
        <Word id="w2"><Coords points="211,10 500,10 500,60 211,60"/><TextEquiv><Unicode>café</Unicode></TextEquiv></Word>
      </TextLine>
    </TextRegion>
  </Page>
</PcGts>` + "\n```"

const pageXMLPrefixedLines = `<pc:PcGts xmlns:pc="http://schema.primaresearch.org/PAGE/gts/pagecontent/2013-07-15">
<pc:Page imageFilename="p.png" imageWidth="10" imageHeight="10">
<pc:TextRegion id="r1"><pc:Coords points="0,0 9,0 9,9 0,9"/>
<pc:TextLine id="l1"><pc:Coords points="0,0 9,0 9,4 0,4"/><pc:TextEquiv><pc:Unicode/></pc:TextEquiv></pc:TextLine>
<pc:TextLine id="l2"><pc:Coords points="0,5 9,5 9,9 0,9"/><pc:TextStyle fontSize="12"/></pc:TextLine>
</pc:TextRegion>
</pc:Page>
</pc:PcGts>`

const pageXMLPrefixedResponse = `<pc:PcGts xmlns:pc="x"><pc:TextLine id="l1"><pc:Coords points="0,0 9,0 9,4 0,4"/><pc:TextEquiv><pc:Unicode>first &lt;line&gt;</pc:Unicode></pc:TextEquiv></pc:TextLine>
<pc:TextLine id="l2"><pc:Coords points="0,5 9,5 9,9 0,9"/><pc:Word id="x"><pc:TextEquiv><pc:Unicode>second</pc:Unicode></pc:TextEquiv></pc:Word><pc:Word id="y"><pc:TextEquiv><pc:Unicode>line</pc:Unicode></pc:TextEquiv></pc:Word></pc:TextLine></pc:PcGts>`

const hocrXHTML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml"><head><title></title></head><body>
<div class="ocr_page" id="page_1" title="bbox 0 0 100 50">
<span class="ocr_line" id="line_1" title="bbox 0 0 100 20; baseline 0 0">
<span class="ocrx_word" id="word_1" title="bbox 0 0 40 20; x_wconf 0"></span>
<span class="ocrx_word" title="bbox 50 0 100 20"></span>
</span>
</div></body></html>`

const hocrHTMLLines = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>&nbsp;</title></head><body>
<div class='ocr_page' title='bbox 0 0 100 50'>
<span class='ocr_line' id='line_1' title='bbox 0 0 100 20'></span><br>
<span class='ocr_line' id='line_2' title='bbox 0 25 100 45'></span>
</div></body></html>`

type fakeClient struct {
	response providers.Result
	err      error
	prompt   string
}

func (f *fakeClient) Name() string { return "fake" }

func (f *fakeClient) Extract(_ context.Context, request providers.Request) (providers.Result, error) {
	f.prompt = request.Prompt
	return f.response, f.err
}

func TestMerge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		template        string
		response        string
		wantFormat      Format
		wantTargets     int
		wantFilled      int
		wantMissing     []string
		wantGeometry    []string
		wantText        string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "pagexml words derive line and region text",
			template:     pageXMLWords,
			response:     pageXMLWordsResponse,
			wantFormat:   FormatPageXML,
			wantTargets:  3,
			wantFilled:   2,
			wantMissing:  []string{"w3"},
			wantGeometry: []string{"w2"},
			wantText:     "Fish & Co café\n",
			wantContains: []string{
				`<Word id="w1"><Coords points="10,10 200,10 200,60 10,60"/><TextEquiv><Unicode>Fish &amp; Co</Unicode></TextEquiv></Word>`,
				`<Word id="w2"><Coords points="210,10 500,10 500,60 210,60"/><TextEquiv><Unicode>café</Unicode></TextEquiv></Word>`,
				"<TextEquiv><Unicode>Fish &amp; Co café</Unicode></TextEquiv></TextLine>",
				"<TextEquiv><Unicode>Fish &amp; Co café</Unicode></TextEquiv></TextRegion>",
				`<Word id="w3"><Coords points="10,70 500,70 500,120 10,120"/></Word>`,
			},
		},
		{
			name:        "prefixed pagexml lines reuse empty unicode and precede text style",
			template:    pageXMLPrefixedLines,
			response:    pageXMLPrefixedResponse,
			wantFormat:  FormatPageXML,
			wantTargets: 2,
			wantFilled:  2,
			wantText:    "first <line>\nsecond line",
			wantContains: []string{
				`<pc:TextEquiv><pc:Unicode>first &lt;line&gt;</pc:Unicode></pc:TextEquiv></pc:TextLine>`,
				`<pc:TextEquiv><pc:Unicode>second line</pc:Unicode></pc:TextEquiv><pc:TextStyle fontSize="12"/>`,
				"<pc:TextEquiv><pc:Unicode>first &lt;line&gt;\nsecond line</pc:Unicode></pc:TextEquiv></pc:TextRegion>",
			},
			wantNotContains: []string{`id="x"`},
		},
		{
			name:        "html hocr lines",
			template:    hocrHTMLLines,
			response:    `<html><body><span class='ocr_line' id='line_1' title='bbox 0 0 100 20'>Dear  Sir,</span><span class='ocr_line' id='line_2' title='bbox 0 25 100 45'>I write &amp; ask</span></body></html>`,
			wantFormat:  FormatHOCR,
			wantTargets: 2,
			wantFilled:  2,
			wantText:    "Dear Sir,\nI write & ask",
			wantContains: []string{
				`<span class='ocr_line' id='line_1' title='bbox 0 0 100 20'>Dear Sir,</span><br>`,
				`<span class='ocr_line' id='line_2' title='bbox 0 25 100 45'>I write &amp; ask</span>`,
				`<meta charset="utf-8"><title>&nbsp;</title>`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := Merge([]byte(tt.template), "", tt.response)
			if err != nil {
				t.Fatal(err)
			}
			if result.Format != tt.wantFormat || result.Targets != tt.wantTargets || result.Filled != tt.wantFilled || result.Text != tt.wantText {
				t.Fatalf("format=%s targets=%d filled=%d text=%q", result.Format, result.Targets, result.Filled, result.Text)
			}
			if !slices.Equal(result.Missing, tt.wantMissing) || !slices.Equal(result.GeometryChanged, tt.wantGeometry) {
				t.Fatalf("missing=%v geometry=%v", result.Missing, result.GeometryChanged)
			}
			document := string(result.Document)
			for _, want := range tt.wantContains {
				if !strings.Contains(document, want) {
					t.Errorf("document missing %q:\n%s", want, document)
				}
			}
			for _, unwanted := range tt.wantNotContains {
				if strings.Contains(document, unwanted) {
					t.Errorf("document contains %q", unwanted)
				}
			}
			if tt.wantFormat == FormatPageXML {
				assertWellFormed(t, result.Document)
			}
		})
	}
}

func TestFillGeneratesMissingIDs(t *testing.T) {
	t.Parallel()
	client := &fakeClient{response: providers.Result{
		Text:           `<html><body><span class="ocrx_word" id="word_1" title="bbox 0 0 40 20; x_wconf 0">Hello</span><span class="ocrx_word" id="htr-1" title="bbox 50 0 100 20">world</span></body></html>`,
		Usage:          providers.UsageInfo{InputTokens: 10, OutputTokens: 5},
		EffectiveModel: "fake-1",
	}}
	result, err := Fill(context.Background(), client, Request{
		Model:      "fake",
		Image:      providers.Image{Data: []byte("png"), MediaType: "image/png"},
		Template:   []byte(hocrXHTML),
		Transcript: "Hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.prompt, `<span id="htr-1" class="ocrx_word" title="bbox 50 0 100 20">`) ||
		!strings.Contains(client.prompt, "<transcript>\nHello world\n</transcript>") {
		t.Fatalf("prompt missing generated id or transcript:\n%s", client.prompt)
	}
	document := string(result.Document)
	if strings.Contains(document, "htr-1") || !strings.Contains(document, `<span class="ocrx_word" title="bbox 50 0 100 20">world</span>`) {
		t.Fatalf("unexpected document:\n%s", document)
	}
	if result.Text != "Hello world" || result.Usage.InputTokens != 10 || result.EffectiveModel != "fake-1" {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertWellFormed(t, result.Document)
}

func TestFillErrors(t *testing.T) {
	t.Parallel()
	upstream := providers.NewError(providers.ErrorRateLimited, 429, true, nil)
	tests := []struct {
		name         string
		client       providers.Client
		template     string
		wantTemplate bool
		wantKind     providers.ErrorKind
		wantUsage    bool
	}{
		{name: "nil client", template: pageXMLWords, wantKind: providers.ErrorInvalidRequest},
		{name: "no lines", client: &fakeClient{}, template: `<PcGts><Page/></PcGts>`, wantTemplate: true},
		{name: "duplicate ids", client: &fakeClient{}, template: `<PcGts><TextLine id="a"/><TextLine id="a"/></PcGts>`, wantTemplate: true},
		{name: "malformed", client: &fakeClient{}, template: `<PcGts><TextLine id="a"></PcGts>`, wantTemplate: true},
		{name: "unknown root", client: &fakeClient{}, template: `<alto/>`, wantTemplate: true},
		{name: "provider error keeps usage", client: &fakeClient{err: upstream, response: providers.Result{Usage: providers.UsageInfo{InputTokens: 3}}}, template: pageXMLWords, wantKind: providers.ErrorRateLimited, wantUsage: true},
		{name: "prose response", client: &fakeClient{response: providers.Result{Text: "I cannot read this", Usage: providers.UsageInfo{InputTokens: 3}}}, template: pageXMLWords, wantKind: providers.ErrorInvalidResponse, wantUsage: true},
		{name: "no ids matched", client: &fakeClient{response: providers.Result{Text: `<PcGts><Word id="zz"/></PcGts>`, Usage: providers.UsageInfo{InputTokens: 3}}}, template: pageXMLWords, wantKind: providers.ErrorInvalidResponse, wantUsage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := Fill(context.Background(), tt.client, Request{
				Model:    "fake",
				Image:    providers.Image{Data: []byte("png"), MediaType: "image/png"},
				Template: []byte(tt.template),
			})
			if tt.wantTemplate {
				if !errors.Is(err, ErrInvalidTemplate) {
					t.Fatalf("err = %v, want ErrInvalidTemplate", err)
				}
				return
			}
			var providerError *providers.Error
			if !errors.As(err, &providerError) || providerError.Kind != tt.wantKind {
				t.Fatalf("err = %v, want %s", err, tt.wantKind)
			}
			if tt.wantUsage && result.Usage.InputTokens != 3 {
				t.Fatalf("usage not preserved: %#v", result.Usage)
			}
		})
	}
}

func TestBuildPromptInstructionsOverride(t *testing.T) {
	t.Parallel()
	parsed, err := parseTemplate([]byte(pageXMLWords), FormatPageXML)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildPrompt(parsed, "Custom task.", "", parsed.source)
	if !strings.HasPrefix(prompt, "Custom task.\n\n<layout>\n<?xml") || strings.Contains(prompt, "<transcript>") {
		t.Fatalf("unexpected prompt:\n%s", prompt)
	}
}

func assertWellFormed(t *testing.T, document []byte) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(string(document)))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatalf("output is not well-formed: %v\n%s", err, document)
		}
	}
}
