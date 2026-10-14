// Package layout fills blank PageXML or hOCR layout templates, such as those
// produced by a segmentation model, with text transcribed by a vision model.
//
// The model receives the page image and the template and is asked to return
// the template unchanged except for added transcriptions. The returned
// document is never trusted for geometry: transcriptions are matched to the
// template by element ID and spliced into the original template bytes, so
// coordinates, IDs, and unrelated markup are always preserved exactly.
package layout

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Format identifies a layout template format.
type Format string

const (
	// FormatPageXML is the PRImA PAGE XML format.
	FormatPageXML Format = "pagexml"
	// FormatHOCR is the hOCR (X)HTML format.
	FormatHOCR Format = "hocr"
)

// ErrInvalidTemplate indicates a template that cannot be filled.
var ErrInvalidTemplate = errors.New("invalid layout template")

const generatedIDPrefix = "htr-"

// Request is the input to Fill.
type Request struct {
	// Model is the provider model identifier.
	Model string
	// Image is the encoded page image the template was segmented from.
	Image providers.Image
	// Template is the blank PageXML or hOCR document.
	Template []byte
	// Format is the template format; empty detects it from the document.
	Format Format
	// Transcript is an optional page transcription to distribute across the
	// boxes instead of transcribing from scratch.
	Transcript string
	// Instructions replaces the default task instructions. The transcript and
	// template are always appended.
	Instructions string
	// Temperature is passed to providers that support it.
	Temperature float64
}

// Result is a filled template and a report on how well the model followed it.
type Result struct {
	// Document is the template with transcriptions written into it.
	Document []byte
	// Format is the template format.
	Format Format
	// Text is the filled transcription in document order, one line per line.
	Text string
	// Targets is the number of boxes (words, or lines without words) to fill.
	Targets int
	// Filled is the number of boxes that received non-empty text.
	Filled int
	// Missing lists target IDs absent from the model response.
	Missing []string
	// GeometryChanged lists target IDs whose coordinates the model altered.
	// The template's coordinates are kept regardless.
	GeometryChanged []string
	// Response is the raw model output.
	Response string
	// Usage is the provider token usage.
	Usage providers.UsageInfo
	// EffectiveModel is the model version reported by the provider.
	EffectiveModel string
}

// target is one box the model must fill.
type target struct {
	id      string
	node    *element
	missing bool // template element had no id; id was generated
}

// template is a parsed layout document.
type template struct {
	source  []byte
	format  Format
	root    *element
	targets []target
}

// DetectFormat reports whether document is PageXML or hOCR.
func DetectFormat(document []byte) (Format, error) {
	decoder := xml.NewDecoder(bytes.NewReader(document))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("%w: no root element", ErrInvalidTemplate)
		}
		if start, ok := token.(xml.StartElement); ok {
			switch strings.ToLower(start.Name.Local) {
			case "pcgts":
				return FormatPageXML, nil
			case "html":
				return FormatHOCR, nil
			default:
				return "", fmt.Errorf("%w: unrecognized root element", ErrInvalidTemplate)
			}
		}
	}
}

// Fill sends the image and template to client and returns the template filled
// with the model's transcriptions. Usage is returned even when the response
// cannot be used, because the call was billed.
func Fill(ctx context.Context, client providers.Client, request Request) (Result, error) {
	if client == nil {
		return Result{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	parsed, err := parseTemplate(request.Template, request.Format)
	if err != nil {
		return Result{}, err
	}
	promptDocument, err := parsed.promptDocument()
	if err != nil {
		return Result{}, err
	}
	response, err := client.Extract(ctx, providers.Request{
		Model:       request.Model,
		Prompt:      buildPrompt(parsed, request.Instructions, request.Transcript, promptDocument),
		Temperature: request.Temperature,
		Image:       request.Image,
	})
	result := Result{
		Format:         parsed.format,
		Targets:        len(parsed.targets),
		Response:       response.Text,
		Usage:          response.Usage,
		EffectiveModel: response.EffectiveModel,
	}
	if err != nil {
		return result, err
	}
	if err := parsed.merge(response.Text, &result); err != nil {
		return result, err
	}
	return result, nil
}

// Merge fills template from a model response obtained elsewhere, such as a
// saved response from an earlier Fill. format may be empty to detect it.
func Merge(templateDocument []byte, format Format, response string) (Result, error) {
	parsed, err := parseTemplate(templateDocument, format)
	if err != nil {
		return Result{}, err
	}
	result := Result{Format: parsed.format, Targets: len(parsed.targets), Response: response}
	if err := parsed.merge(response, &result); err != nil {
		return result, err
	}
	return result, nil
}

func parseTemplate(document []byte, format Format) (*template, error) {
	if format == "" {
		detected, err := DetectFormat(document)
		if err != nil {
			return nil, err
		}
		format = detected
	}
	var root *element
	var err error
	switch format {
	case FormatPageXML:
		root, err = parseDocument(document, true, false)
	case FormatHOCR:
		root, err = parseDocument(document, true, false)
		if err != nil {
			// Plain HTML hOCR is not well-formed XML; offsets remain exact for
			// the tags the lenient decoder does see.
			root, err = parseDocument(document, false, true)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported format %q", ErrInvalidTemplate, format)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: malformed XML", ErrInvalidTemplate)
	}
	parsed := &template{source: document, format: format, root: root}
	if err := parsed.collectTargets(); err != nil {
		return nil, err
	}
	return parsed, nil
}

// collectTargets finds the boxes to fill: words where a line is segmented
// into words, otherwise the line itself.
func (t *template) collectTargets() error {
	var nodes []*element
	switch t.format {
	case FormatPageXML:
		t.root.walk(func(e *element) {
			if e.local != "TextLine" {
				return
			}
			if words := e.childrenNamed("Word"); len(words) > 0 {
				nodes = append(nodes, words...)
				return
			}
			nodes = append(nodes, e)
		})
	case FormatHOCR:
		t.root.walk(func(e *element) {
			if e.hasClass("ocrx_word") || (isHOCRLine(e) && !hasHOCRWords(e)) {
				nodes = append(nodes, e)
			}
		})
	}
	if len(nodes) == 0 {
		return fmt.Errorf("%w: no text lines or words", ErrInvalidTemplate)
	}
	seen := make(map[string]bool)
	t.root.walk(func(e *element) {
		if id := e.attrs["id"]; id != "" {
			seen[id] = true
		}
	})
	used := make(map[string]bool, len(nodes))
	next := 1
	for _, node := range nodes {
		id := node.attrs["id"]
		generated := id == ""
		if generated {
			for id = generatedIDPrefix + strconv.Itoa(next); seen[id]; id = generatedIDPrefix + strconv.Itoa(next) {
				next++
			}
			next++
			seen[id] = true
		}
		if used[id] {
			return fmt.Errorf("%w: duplicate element id", ErrInvalidTemplate)
		}
		used[id] = true
		t.targets = append(t.targets, target{id: id, node: node, missing: generated})
	}
	return nil
}

// promptDocument returns the template with generated IDs added to targets
// that lack one, so every box can be matched in the response.
func (t *template) promptDocument() ([]byte, error) {
	var edits []edit
	for _, target := range t.targets {
		if target.missing {
			position := target.node.start + 1 + len(target.node.rawName)
			edits = append(edits, edit{position, position, ` id="` + target.id + `"`})
		}
	}
	if len(edits) == 0 {
		return t.source, nil
	}
	return applyEdits(t.source, edits)
}

// merge writes the response's transcriptions into the template.
func (t *template) merge(response string, result *Result) error {
	start, end := strings.IndexByte(response, '<'), strings.LastIndexByte(response, '>')
	if start < 0 || end < start {
		return providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
	}
	responseRoot, _ := parseDocument([]byte(response[start:end+1]), false, t.format == FormatHOCR)
	byID := make(map[string]*element)
	responseRoot.walk(func(e *element) {
		if id := e.attrs["id"]; id != "" && byID[id] == nil {
			byID[id] = e
		}
	})

	texts := make(map[*element]string, len(t.targets))
	for _, target := range t.targets {
		answer := byID[target.id]
		if answer == nil {
			result.Missing = append(result.Missing, target.id)
			continue
		}
		if t.geometry(answer) != t.geometry(target.node) {
			result.GeometryChanged = append(result.GeometryChanged, target.id)
		}
		if text := t.responseText(answer); text != "" {
			texts[target.node] = text
			result.Filled++
		}
	}
	if len(result.Missing) == len(t.targets) {
		return providers.NewError(providers.ErrorInvalidResponse, 0, false, nil)
	}

	var edits []edit
	var lines []string
	switch t.format {
	case FormatPageXML:
		edits, lines = t.pageXMLEdits(texts)
	case FormatHOCR:
		edits, lines = t.hocrEdits(texts)
	}
	document, err := applyEdits(t.source, edits)
	if err != nil {
		return err
	}
	result.Document = document
	result.Text = strings.Join(lines, "\n")
	return nil
}

// responseText extracts a box's transcription from the model's element.
func (t *template) responseText(e *element) string {
	if t.format == FormatHOCR {
		return strings.Join(strings.Fields(e.text.String()), " ")
	}
	if text := pageXMLText(e); text != "" {
		return text
	}
	// A model asked to fill a line sometimes segments it into words itself.
	var words []string
	for _, word := range e.childrenNamed("Word") {
		if text := pageXMLText(word); text != "" {
			words = append(words, text)
		}
	}
	return strings.Join(words, " ")
}

// pageXMLText returns the element's own TextEquiv/Unicode text.
func pageXMLText(e *element) string {
	equiv := e.child("TextEquiv")
	if equiv == nil {
		return ""
	}
	unicode := equiv.child("Unicode")
	if unicode == nil {
		return ""
	}
	return strings.TrimSpace(unicode.text.String())
}

// geometry returns a normalized representation of the element's box.
func (t *template) geometry(e *element) string {
	if t.format == FormatPageXML {
		if coords := e.child("Coords"); coords != nil {
			return strings.Join(strings.Fields(coords.attrs["points"]), " ")
		}
		return ""
	}
	for _, property := range strings.Split(e.attrs["title"], ";") {
		if fields := strings.Fields(property); len(fields) > 0 && fields[0] == "bbox" {
			return strings.Join(fields, " ")
		}
	}
	return ""
}

// pageXMLEdits writes target text and derives line and region text from it,
// keeping the PAGE hierarchy consistent.
func (t *template) pageXMLEdits(texts map[*element]string) ([]edit, []string) {
	var edits []edit
	var lines []string
	lineText := make(map[*element]string)
	t.root.walk(func(e *element) {
		if e.local != "TextLine" {
			return
		}
		text := texts[e]
		if words := e.childrenNamed("Word"); len(words) > 0 {
			var parts []string
			for _, word := range words {
				if wordText := texts[word]; wordText != "" {
					edits = append(edits, t.setTextEquiv(word, wordText))
					parts = append(parts, wordText)
				}
			}
			text = strings.Join(parts, " ")
		}
		if text != "" {
			edits = append(edits, t.setTextEquiv(e, text))
			lineText[e] = text
		}
		lines = append(lines, text)
	})
	t.root.walk(func(e *element) {
		if e.local != "TextRegion" {
			return
		}
		var parts []string
		for _, line := range e.childrenNamed("TextLine") {
			if text := lineText[line]; text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			edits = append(edits, t.setTextEquiv(e, strings.Join(parts, "\n")))
		}
	})
	return edits, lines
}

// pageXMLTrailing lists children that the PAGE schema orders after TextEquiv.
var pageXMLTrailing = map[string]bool{"TextStyle": true, "UserDefined": true, "Labels": true}

// setTextEquiv returns an edit setting e's TextEquiv/Unicode text.
func (t *template) setTextEquiv(e *element, text string) edit {
	prefix := e.prefix()
	unicodeMarkup := "<" + prefix + "Unicode>" + escape(text) + "</" + prefix + "Unicode>"
	if equiv := e.child("TextEquiv"); equiv != nil {
		if unicode := equiv.child("Unicode"); unicode != nil {
			return replaceContent(unicode, t.source, text)
		}
		return insertContent(equiv, t.source, equiv.end, unicodeMarkup)
	}
	position := e.end
	for _, child := range e.children {
		if pageXMLTrailing[child.local] {
			position = child.start
			break
		}
	}
	return insertContent(e, t.source, position, "<"+prefix+"TextEquiv>"+unicodeMarkup+"</"+prefix+"TextEquiv>")
}

// hocrEdits writes target text into hOCR word or line elements.
func (t *template) hocrEdits(texts map[*element]string) ([]edit, []string) {
	var edits []edit
	var lines []string
	lineWords := make(map[*element][]string)
	var lineOrder []*element
	for _, target := range t.targets {
		text := texts[target.node]
		if text != "" {
			edits = append(edits, replaceContent(target.node, t.source, text))
		}
		line := target.node
		if !isHOCRLine(line) {
			for line = target.node.parent; line != nil && !isHOCRLine(line); line = line.parent {
			}
			if line == nil {
				line = target.node
			}
		}
		if _, seen := lineWords[line]; !seen {
			lineOrder = append(lineOrder, line)
			lineWords[line] = nil
		}
		if text != "" {
			lineWords[line] = append(lineWords[line], text)
		}
	}
	for _, line := range lineOrder {
		lines = append(lines, strings.Join(lineWords[line], " "))
	}
	return edits, lines
}

func isHOCRLine(e *element) bool {
	return e.hasClass("ocr_line", "ocr_textfloat", "ocr_header", "ocr_caption")
}

func hasHOCRWords(e *element) bool {
	found := false
	e.walk(func(child *element) { found = found || child.hasClass("ocrx_word") })
	return found
}
