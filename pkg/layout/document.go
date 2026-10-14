package layout

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// element is one parsed XML element plus the byte offsets of its tags in the
// source document, so edits can be spliced into the original bytes without
// re-serializing (encoding/xml cannot round-trip namespace prefixes).
type element struct {
	local       string
	rawName     string
	attrs       map[string]string
	start       int
	startEnd    int
	end         int
	endEnd      int
	selfClosing bool
	parent      *element
	children    []*element
	text        strings.Builder
}

// parseDocument parses data into an element tree rooted at a synthetic
// document node. Lenient parsing returns whatever was parsed before a syntax
// error so that partially malformed model output can still be harvested.
func parseDocument(data []byte, strict, html bool) (*element, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = strict
	if html {
		decoder.AutoClose = xml.HTMLAutoClose
		decoder.Entity = xml.HTMLEntity
	}
	root := &element{local: "#document"}
	stack := []*element{root}
	for {
		position := int(decoder.InputOffset())
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if strict {
				return nil, err
			}
			break
		}
		after := int(decoder.InputOffset())
		switch typed := token.(type) {
		case xml.StartElement:
			parent := stack[len(stack)-1]
			current := &element{
				local:    typed.Name.Local,
				rawName:  rawName(data, position, typed.Name.Local),
				attrs:    make(map[string]string, len(typed.Attr)),
				start:    position,
				startEnd: after,
				parent:   parent,
			}
			for _, attr := range typed.Attr {
				if attr.Name.Space == "" {
					current.attrs[attr.Name.Local] = attr.Value
				}
			}
			parent.children = append(parent.children, current)
			stack = append(stack, current)
		case xml.EndElement:
			if len(stack) == 1 {
				if strict {
					return nil, errors.New("unbalanced end element")
				}
				continue
			}
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			current.end, current.endEnd = position, after
			current.selfClosing = position == after
		case xml.CharData:
			for _, open := range stack[1:] {
				open.text.Write(typed)
			}
		}
	}
	if strict && len(stack) != 1 {
		return nil, errors.New("unclosed element")
	}
	return root, nil
}

// rawName returns the element name as written, including any namespace prefix.
func rawName(data []byte, position int, local string) string {
	if position < 0 || position >= len(data) || data[position] != '<' {
		return local
	}
	end := position + 1
	for end < len(data) && !strings.ContainsRune(" \t\r\n/>", rune(data[end])) {
		end++
	}
	name := string(data[position+1 : end])
	if name != local && !strings.HasSuffix(name, ":"+local) {
		return local
	}
	return name
}

// walk visits every element in document order.
func (e *element) walk(visit func(*element)) {
	for _, child := range e.children {
		visit(child)
		child.walk(visit)
	}
}

// child returns the first direct child with the given local name.
func (e *element) child(local string) *element {
	for _, child := range e.children {
		if child.local == local {
			return child
		}
	}
	return nil
}

// childrenNamed returns the direct children with the given local name.
func (e *element) childrenNamed(local string) []*element {
	var matches []*element
	for _, child := range e.children {
		if child.local == local {
			matches = append(matches, child)
		}
	}
	return matches
}

// hasClass reports whether the element's class attribute contains any of names.
func (e *element) hasClass(names ...string) bool {
	for class := range strings.FieldsSeq(e.attrs["class"]) {
		if slices.Contains(names, class) {
			return true
		}
	}
	return false
}

// prefix returns the namespace prefix including the colon, or "".
func (e *element) prefix() string {
	if i := strings.IndexByte(e.rawName, ':'); i >= 0 {
		return e.rawName[:i+1]
	}
	return ""
}

// edit replaces source[start:end] with text.
type edit struct {
	start, end int
	text       string
}

// applyEdits splices non-overlapping edits into source.
func applyEdits(source []byte, edits []edit) ([]byte, error) {
	slices.SortStableFunc(edits, func(a, b edit) int { return b.start - a.start })
	output := slices.Clone(source)
	limit := len(source)
	for _, change := range edits {
		if change.start < 0 || change.end < change.start || change.end > limit {
			return nil, fmt.Errorf("overlapping edit at offset %d", change.start)
		}
		output = slices.Concat(output[:change.start], []byte(change.text), output[change.end:])
		limit = change.start
	}
	return output, nil
}

// replaceContent returns an edit that replaces the element's content with
// escaped text, expanding a self-closing tag when necessary.
func replaceContent(e *element, source []byte, text string) edit {
	if e.selfClosing {
		return edit{e.start, e.endEnd, openTag(e, source) + escape(text) + "</" + e.rawName + ">"}
	}
	return edit{e.startEnd, e.end, escape(text)}
}

// insertContent returns an edit that inserts markup at position inside e,
// expanding a self-closing tag when necessary.
func insertContent(e *element, source []byte, position int, markup string) edit {
	if e.selfClosing {
		return edit{e.start, e.endEnd, openTag(e, source) + markup + "</" + e.rawName + ">"}
	}
	return edit{position, position, markup}
}

// openTag converts a self-closing start tag into an open tag.
func openTag(e *element, source []byte) string {
	tag := strings.TrimSuffix(string(source[e.start:e.startEnd]), "/>")
	return strings.TrimRight(tag, " \t\r\n") + ">"
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escape(text string) string { return escaper.Replace(text) }
