package hocr

import (
	"testing"
)

func TestGroupWordsByLines(t *testing.T) {
	tests := []struct {
		name          string
		words         []WordImage
		expectedLines int
	}{
		{
			name:          "empty words",
			words:         []WordImage{},
			expectedLines: 0,
		},
		{
			name: "single word",
			words: []WordImage{
				{
					Index: 0,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 30}, {X: 10, Y: 30}},
					},
				},
			},
			expectedLines: 1,
		},
		{
			name: "two words same line",
			words: []WordImage{
				{
					Index: 0,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 30}, {X: 10, Y: 30}},
					},
				},
				{
					Index: 1,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 50, Y: 12}, {X: 80, Y: 12}, {X: 80, Y: 32}, {X: 50, Y: 32}},
					},
				},
			},
			expectedLines: 1,
		},
		{
			name: "two words different lines",
			words: []WordImage{
				{
					Index: 0,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 30}, {X: 10, Y: 30}},
					},
				},
				{
					Index: 1,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 50}, {X: 40, Y: 50}, {X: 40, Y: 70}, {X: 10, Y: 70}},
					},
				},
			},
			expectedLines: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupWordsByLines(tt.words)
			if len(result) != tt.expectedLines {
				t.Errorf("groupWordsByLines() returned %d lines, want %d", len(result), tt.expectedLines)
			}
		})
	}
}

func TestDistributeLineTextToWords(t *testing.T) {
	tests := []struct {
		name          string
		lineText      string
		wordCount     int
		expectedTexts []string
	}{
		{
			name:          "same number of words",
			lineText:      "hello world test",
			wordCount:     3,
			expectedTexts: []string{"hello", "world", "test"},
		},
		{
			name:          "more detected than transcribed",
			lineText:      "hello world",
			wordCount:     3,
			expectedTexts: []string{"hello", "world", ""},
		},
		{
			name:          "more transcribed than detected",
			lineText:      "hello world this is test",
			wordCount:     3,
			expectedTexts: []string{"hello world", "this is", "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create word images
			words := make([]*WordImage, tt.wordCount)
			for i := 0; i < tt.wordCount; i++ {
				words[i] = &WordImage{Index: i}
			}

			distributeLineTextToWords(tt.lineText, words)

			for i, word := range words {
				if word.Text != tt.expectedTexts[i] {
					t.Errorf("word %d: got %q, want %q", i, word.Text, tt.expectedTexts[i])
				}
			}
		})
	}
}

func TestBuildHOCRFromWords(t *testing.T) {
	tests := []struct {
		name          string
		wordImages    []WordImage
		expectedEmpty bool
	}{
		{
			name:          "empty words",
			wordImages:    []WordImage{},
			expectedEmpty: true,
		},
		{
			name: "words without text",
			wordImages: []WordImage{
				{
					Index: 0,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 30}, {X: 10, Y: 30}},
					},
					Text: "",
				},
			},
			expectedEmpty: true,
		},
		{
			name: "words with text",
			wordImages: []WordImage{
				{
					Index: 0,
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 30}, {X: 10, Y: 30}},
					},
					Text: "hello",
				},
			},
			expectedEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildHOCRFromWords(tt.wordImages)
			isEmpty := len(result) == 0
			if isEmpty != tt.expectedEmpty {
				t.Errorf("buildHOCRFromWords() isEmpty = %v, want %v", isEmpty, tt.expectedEmpty)
			}
		})
	}
}
