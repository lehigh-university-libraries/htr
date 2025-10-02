package hocr

import (
	"testing"
)

func TestIsTextPixel(t *testing.T) {
	tests := []struct {
		name     string
		r, g, b  uint32
		expected bool
	}{
		{"black pixel", 0, 0, 0, true},
		{"dark gray pixel", 20000, 20000, 20000, true},
		{"white pixel", 65535, 65535, 65535, false},
		{"light gray pixel", 40000, 40000, 40000, false},
		{"threshold boundary", 32767, 32767, 32767, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a simple color struct that implements color.Color
			c := testColor{r: tt.r, g: tt.g, b: tt.b}
			result := isTextPixel(c)
			if result != tt.expected {
				t.Errorf("isTextPixel() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsValidWordSize(t *testing.T) {
	tests := []struct {
		name                      string
		w, h, imgWidth, imgHeight int
		expected                  bool
	}{
		{"valid word size", 50, 20, 1000, 800, true},
		{"too small width", 5, 20, 1000, 800, false},
		{"too small height", 50, 5, 1000, 800, false},
		{"too large width", 600, 20, 1000, 800, false},
		{"too large height", 50, 200, 1000, 800, false},
		{"minimum valid size", 8, 10, 1000, 800, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidWordSize(tt.w, tt.h, tt.imgWidth, tt.imgHeight)
			if result != tt.expected {
				t.Errorf("isValidWordSize(%d, %d, %d, %d) = %v, want %v",
					tt.w, tt.h, tt.imgWidth, tt.imgHeight, result, tt.expected)
			}
		})
	}
}

func TestShouldMergeComponents(t *testing.T) {
	tests := []struct {
		name     string
		a, b     WordBox
		expected bool
	}{
		{
			name:     "close horizontal words",
			a:        WordBox{X: 10, Y: 10, Width: 30, Height: 20},
			b:        WordBox{X: 45, Y: 12, Width: 30, Height: 20},
			expected: true,
		},
		{
			name:     "far apart horizontal words",
			a:        WordBox{X: 10, Y: 10, Width: 30, Height: 20},
			b:        WordBox{X: 100, Y: 12, Width: 30, Height: 20},
			expected: false,
		},
		{
			name:     "different vertical lines",
			a:        WordBox{X: 10, Y: 10, Width: 30, Height: 20},
			b:        WordBox{X: 45, Y: 100, Width: 30, Height: 20},
			expected: false,
		},
		{
			name:     "overlapping words",
			a:        WordBox{X: 10, Y: 10, Width: 30, Height: 20},
			b:        WordBox{X: 25, Y: 12, Width: 30, Height: 20},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldMergeComponents(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("shouldMergeComponents() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMergeComponentGroup(t *testing.T) {
	tests := []struct {
		name     string
		group    []WordBox
		expected WordBox
	}{
		{
			name: "single component",
			group: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20, Text: "word1"},
			},
			expected: WordBox{X: 10, Y: 10, Width: 30, Height: 20, Text: "word1"},
		},
		{
			name: "two adjacent components",
			group: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
				{X: 45, Y: 12, Width: 30, Height: 18},
			},
			expected: WordBox{X: 10, Y: 10, Width: 65, Height: 20},
		},
		{
			name: "three components with varying heights",
			group: []WordBox{
				{X: 10, Y: 10, Width: 20, Height: 20},
				{X: 35, Y: 8, Width: 20, Height: 24},
				{X: 60, Y: 12, Width: 20, Height: 18},
			},
			expected: WordBox{X: 10, Y: 8, Width: 70, Height: 24},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeComponentGroup(tt.group)
			if result.X != tt.expected.X || result.Y != tt.expected.Y ||
				result.Width != tt.expected.Width || result.Height != tt.expected.Height {
				t.Errorf("mergeComponentGroup() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestGroupWordsIntoLines(t *testing.T) {
	tests := []struct {
		name          string
		words         []WordBox
		expectedLines int
	}{
		{
			name:          "empty words",
			words:         []WordBox{},
			expectedLines: 0,
		},
		{
			name: "single word",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
			},
			expectedLines: 1,
		},
		{
			name: "two words same line",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
				{X: 50, Y: 12, Width: 30, Height: 20},
			},
			expectedLines: 1,
		},
		{
			name: "two words different lines",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
				{X: 10, Y: 50, Width: 30, Height: 20},
			},
			expectedLines: 2,
		},
		{
			name: "multiple words on multiple lines",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
				{X: 50, Y: 12, Width: 30, Height: 20},
				{X: 10, Y: 50, Width: 30, Height: 20},
				{X: 50, Y: 52, Width: 30, Height: 20},
			},
			expectedLines: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupWordsIntoLines(tt.words)
			if len(result) != tt.expectedLines {
				t.Errorf("groupWordsIntoLines() returned %d lines, want %d", len(result), tt.expectedLines)
			}
		})
	}
}

func TestWordsOnSameLine(t *testing.T) {
	tests := []struct {
		name     string
		line     []WordBox
		newWord  WordBox
		expected bool
	}{
		{
			name:     "empty line",
			line:     []WordBox{},
			newWord:  WordBox{X: 10, Y: 10, Width: 30, Height: 20},
			expected: true,
		},
		{
			name: "word on same line",
			line: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
			},
			newWord:  WordBox{X: 50, Y: 12, Width: 30, Height: 20},
			expected: true,
		},
		{
			name: "word on different line",
			line: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
			},
			newWord:  WordBox{X: 10, Y: 100, Width: 30, Height: 20},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wordsOnSameLine(tt.line, tt.newWord)
			if result != tt.expected {
				t.Errorf("wordsOnSameLine() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCreateLineFromWords(t *testing.T) {
	tests := []struct {
		name     string
		words    []WordBox
		expected LineBox
	}{
		{
			name:     "empty words",
			words:    []WordBox{},
			expected: LineBox{},
		},
		{
			name: "single word",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
			},
			expected: LineBox{
				X:      10,
				Y:      10,
				Width:  30,
				Height: 20,
			},
		},
		{
			name: "multiple words",
			words: []WordBox{
				{X: 10, Y: 10, Width: 30, Height: 20},
				{X: 50, Y: 12, Width: 30, Height: 18},
				{X: 90, Y: 8, Width: 30, Height: 24},
			},
			expected: LineBox{
				X:      10,
				Y:      8,
				Width:  110,
				Height: 24,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createLineFromWords(tt.words)
			if result.X != tt.expected.X || result.Y != tt.expected.Y ||
				result.Width != tt.expected.Width || result.Height != tt.expected.Height {
				t.Errorf("createLineFromWords() = %+v, want %+v", result, tt.expected)
			}
			if len(tt.words) > 0 && len(result.Words) != len(tt.words) {
				t.Errorf("createLineFromWords() returned %d words, want %d", len(result.Words), len(tt.words))
			}
		})
	}
}

func TestConvertWordsAndLinesToOCRResponse(t *testing.T) {
	tests := []struct {
		name          string
		lines         []LineBox
		width, height int
	}{
		{
			name:   "empty lines",
			lines:  []LineBox{},
			width:  800,
			height: 600,
		},
		{
			name: "single line",
			lines: []LineBox{
				{
					X:      10,
					Y:      10,
					Width:  100,
					Height: 20,
					Words: []WordBox{
						{X: 10, Y: 10, Width: 100, Height: 20},
					},
				},
			},
			width:  800,
			height: 600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertWordsAndLinesToOCRResponse(tt.lines, tt.width, tt.height)
			if len(result.Responses) != 1 {
				t.Errorf("convertWordsAndLinesToOCRResponse() returned %d responses, want 1", len(result.Responses))
			}
			if result.Responses[0].FullTextAnnotation == nil {
				t.Errorf("convertWordsAndLinesToOCRResponse() returned nil FullTextAnnotation")
			}
		})
	}
}

func TestMax(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		expected int
	}{
		{"a greater", 10, 5, 10},
		{"b greater", 5, 10, 10},
		{"equal", 5, 5, 5},
		{"negative numbers", -5, -10, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := max(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("max(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		name     string
		x        int
		expected int
	}{
		{"positive", 5, 5},
		{"negative", -5, 5},
		{"zero", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := abs(tt.x)
			if result != tt.expected {
				t.Errorf("abs(%d) = %d, want %d", tt.x, result, tt.expected)
			}
		})
	}
}

// testColor is a simple color implementation for testing
type testColor struct {
	r, g, b uint32
}

func (c testColor) RGBA() (r, g, b, a uint32) {
	return c.r, c.g, c.b, 65535
}
