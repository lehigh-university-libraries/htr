package hocr

import (
	"strings"
	"testing"
)

func TestConvertToBasicHOCR(t *testing.T) {
	tests := []struct {
		name     string
		response OCRResponse
		wantText string
	}{
		{
			name:     "empty response",
			response: OCRResponse{},
			wantText: "",
		},
		{
			name: "response with no annotation",
			response: OCRResponse{
				Responses: []Response{
					{FullTextAnnotation: nil},
				},
			},
			wantText: "",
		},
		{
			name: "response with word",
			response: OCRResponse{
				Responses: []Response{
					{
						FullTextAnnotation: &FullTextAnnotation{
							Pages: []Page{
								{
									Width:  800,
									Height: 600,
									Blocks: []Block{
										{
											Paragraphs: []Paragraph{
												{
													Words: []Word{
														{
															BoundingBox: BoundingPoly{
																Vertices: []Vertex{
																	{X: 10, Y: 10},
																	{X: 40, Y: 10},
																	{X: 40, Y: 30},
																	{X: 10, Y: 30},
																},
															},
															Symbols: []Symbol{
																{Text: "hello"},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantText: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertToBasicHOCR(tt.response)
			if tt.wantText != "" && !strings.Contains(result, tt.wantText) {
				t.Errorf("ConvertToBasicHOCR() missing expected text %q", tt.wantText)
			}
			if !strings.Contains(result, "<!DOCTYPE html") {
				t.Errorf("ConvertToBasicHOCR() missing DOCTYPE declaration")
			}
		})
	}
}

func TestWrapInHOCRDocument(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"empty content", ""},
		{"simple content", "<span>test</span>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapInHOCRDocument(tt.content)
			if !strings.Contains(result, "<!DOCTYPE html") {
				t.Errorf("WrapInHOCRDocument() missing DOCTYPE")
			}
			if !strings.Contains(result, tt.content) {
				t.Errorf("WrapInHOCRDocument() missing content")
			}
			if !strings.Contains(result, "ocr-system") {
				t.Errorf("WrapInHOCRDocument() missing ocr-system meta")
			}
		})
	}
}

func TestCleanProviderResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no special characters",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "ampersand to escape",
			input:    "hello & world",
			expected: "hello &amp; world",
		},
		{
			name:     "already escaped ampersand",
			input:    "hello &amp; world",
			expected: "hello &amp; world",
		},
		{
			name:     "numeric entity",
			input:    "hello &#39; world",
			expected: "hello &#39; world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanProviderResponse(tt.input)
			if result != tt.expected {
				t.Errorf("CleanProviderResponse() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFixAmpersands(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unescaped ampersand",
			input:    "A & B",
			expected: "A &amp; B",
		},
		{
			name:     "already escaped",
			input:    "A &amp; B",
			expected: "A &amp; B",
		},
		{
			name:     "multiple ampersands",
			input:    "A & B & C",
			expected: "A &amp; B &amp; C",
		},
		{
			name:     "valid entity",
			input:    "less than &lt; sign",
			expected: "less than &lt; sign",
		},
		{
			name:     "numeric entity",
			input:    "apostrophe &#39; here",
			expected: "apostrophe &#39; here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fixAmpersands(tt.input)
			if result != tt.expected {
				t.Errorf("fixAmpersands() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestEscapeTextContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no spans",
			input:    "plain text",
			expected: "plain text",
		},
		{
			name:     "span with safe text",
			input:    "<span>hello</span>",
			expected: "<span>hello</span>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeTextContent(tt.input)
			if result != tt.expected {
				t.Errorf("escapeTextContent() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestEscapeTextInSpans(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "span with safe text",
			input:    "<span>safe text</span>",
			expected: "<span>safe text</span>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeTextInSpans(tt.input)
			if result != tt.expected {
				t.Errorf("escapeTextInSpans() = %q, want %q", result, tt.expected)
			}
		})
	}
}
