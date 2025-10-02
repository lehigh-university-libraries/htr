package hocr

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DetectWordBoundariesCustom uses custom image processing algorithm to find word boundaries
func DetectWordBoundariesCustom(imagePath string) (OCRResponse, error) {
	// Get image dimensions first
	width, height, err := getImageDimensions(imagePath)
	if err != nil {
		return OCRResponse{}, fmt.Errorf("failed to get image dimensions: %w", err)
	}

	// Step 1: Detect individual words using image processing
	words, err := detectWords(imagePath, width, height)
	if err != nil {
		return OCRResponse{}, fmt.Errorf("failed to detect words: %w", err)
	}

	slog.Info("Custom word detection completed", "word_count", len(words), "image_size", fmt.Sprintf("%dx%d", width, height))

	// Step 2: Group words into lines based on coordinates
	lines := groupWordsIntoLines(words)
	slog.Info("Grouped words into lines", "line_count", len(lines))

	// Step 3: Convert to OCR response format
	return convertWordsAndLinesToOCRResponse(lines, width, height), nil
}

func getImageDimensions(imagePath string) (int, int, error) {
	cmd := exec.Command("magick", "identify", "-format", "%w %h", imagePath)
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get image dimensions: %w", err)
	}

	var width, height int
	_, err = fmt.Sscanf(strings.TrimSpace(string(output)), "%d %d", &width, &height)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse dimensions: %w", err)
	}

	return width, height, nil
}

func detectWords(imagePath string, imgWidth, imgHeight int) ([]WordBox, error) {
	// Preprocess the image
	processedPath, err := preprocessImageForWordDetection(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess image: %w", err)
	}
	defer os.Remove(processedPath)

	// Load processed image
	file, err := os.Open(processedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open processed image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode processed image: %w", err)
	}

	// Find connected components (potential words)
	components := findWordComponents(img)

	// Filter and refine components to get word boxes
	wordBoxes := refineComponentsToWords(components, imgWidth, imgHeight)

	return wordBoxes, nil
}

func preprocessImageForWordDetection(imagePath string) (string, error) {
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	processedPath := filepath.Join(tempDir, fmt.Sprintf("processed_words_%s_%d.jpg", baseName, time.Now().Unix()))

	cmd := exec.Command("magick", imagePath,
		"-colorspace", "Gray",
		"-contrast-stretch", "0.15x0.05%",
		"-sharpen", "0x1",
		"-morphology", "close", "rectangle:2x1",
		"-threshold", "75%",
		processedPath)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("imagemagick preprocessing failed: %w", err)
	}

	return processedPath, nil
}

func findWordComponents(img image.Image) []WordBox {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	visited := make([][]bool, height)
	for i := range visited {
		visited[i] = make([]bool, width)
	}

	var components []WordBox

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if !visited[y][x] && isTextPixel(img.At(x, y)) {
				minX, minY, maxX, maxY := x, y, x, y
				floodFillComponent(img, visited, x, y, &minX, &minY, &maxX, &maxY)

				w := maxX - minX + 1
				h := maxY - minY + 1
				if isValidWordSize(w, h, width, height) {
					components = append(components, WordBox{
						X:      minX,
						Y:      minY,
						Width:  w,
						Height: h,
						Text:   fmt.Sprintf("word_%d", len(components)+1),
					})
				}
			}
		}
	}

	return components
}

func floodFillComponent(img image.Image, visited [][]bool, x, y int, minX, minY, maxX, maxY *int) {
	bounds := img.Bounds()
	if x < 0 || x >= bounds.Dx() || y < 0 || y >= bounds.Dy() || visited[y][x] || !isTextPixel(img.At(x, y)) {
		return
	}

	visited[y][x] = true

	if x < *minX {
		*minX = x
	}
	if x > *maxX {
		*maxX = x
	}
	if y < *minY {
		*minY = y
	}
	if y > *maxY {
		*maxY = y
	}

	directions := [][]int{{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 1}, {1, -1}, {1, 0}, {1, 1}}
	for _, dir := range directions {
		floodFillComponent(img, visited, x+dir[0], y+dir[1], minX, minY, maxX, maxY)
	}
}

func isTextPixel(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	gray := (r + g + b) / 3
	return gray < 32768
}

func isValidWordSize(w, h, imgWidth, imgHeight int) bool {
	minWidth, minHeight := 8, 10
	maxWidth := imgWidth / 2
	maxHeight := imgHeight / 5
	return w >= minWidth && h >= minHeight && w <= maxWidth && h <= maxHeight
}

func refineComponentsToWords(components []WordBox, imgWidth, imgHeight int) []WordBox {
	if len(components) == 0 {
		return components
	}

	sort.Slice(components, func(i, j int) bool {
		if abs(components[i].Y-components[j].Y) < 10 {
			return components[i].X < components[j].X
		}
		return components[i].Y < components[j].Y
	})

	return mergeNearbyComponents(components)
}

func mergeNearbyComponents(components []WordBox) []WordBox {
	if len(components) <= 1 {
		return components
	}

	var mergedWords []WordBox
	currentGroup := []WordBox{components[0]}

	for i := 1; i < len(components); i++ {
		component := components[i]
		lastInGroup := currentGroup[len(currentGroup)-1]

		if shouldMergeComponents(lastInGroup, component) {
			currentGroup = append(currentGroup, component)
		} else {
			mergedWord := mergeComponentGroup(currentGroup)
			mergedWords = append(mergedWords, mergedWord)
			currentGroup = []WordBox{component}
		}
	}

	if len(currentGroup) > 0 {
		mergedWord := mergeComponentGroup(currentGroup)
		mergedWords = append(mergedWords, mergedWord)
	}

	return mergedWords
}

func shouldMergeComponents(a, b WordBox) bool {
	horizontalGap := b.X - (a.X + a.Width)
	verticalOverlap := b.Y+b.Height >= a.Y && b.Y <= a.Y+a.Height
	maxGap := max(a.Height, b.Height) / 3
	return horizontalGap >= 0 && horizontalGap <= maxGap && verticalOverlap
}

func mergeComponentGroup(group []WordBox) WordBox {
	if len(group) == 1 {
		return group[0]
	}

	minX, minY := group[0].X, group[0].Y
	maxX, maxY := group[0].X+group[0].Width, group[0].Y+group[0].Height

	for _, comp := range group[1:] {
		if comp.X < minX {
			minX = comp.X
		}
		if comp.Y < minY {
			minY = comp.Y
		}
		if comp.X+comp.Width > maxX {
			maxX = comp.X + comp.Width
		}
		if comp.Y+comp.Height > maxY {
			maxY = comp.Y + comp.Height
		}
	}

	return WordBox{
		X:      minX,
		Y:      minY,
		Width:  maxX - minX,
		Height: maxY - minY,
		Text:   fmt.Sprintf("merged_word_%d", len(group)),
	}
}

func groupWordsIntoLines(words []WordBox) []LineBox {
	if len(words) == 0 {
		return nil
	}

	sort.Slice(words, func(i, j int) bool {
		if abs(words[i].Y-words[j].Y) < words[i].Height/2 {
			return words[i].X < words[j].X
		}
		return words[i].Y < words[j].Y
	})

	var lines []LineBox
	var currentLineWords []WordBox

	for _, word := range words {
		if len(currentLineWords) == 0 {
			currentLineWords = append(currentLineWords, word)
			continue
		}

		if wordsOnSameLine(currentLineWords, word) {
			currentLineWords = append(currentLineWords, word)
		} else {
			if len(currentLineWords) > 0 {
				line := createLineFromWords(currentLineWords)
				lines = append(lines, line)
			}
			currentLineWords = []WordBox{word}
		}
	}

	if len(currentLineWords) > 0 {
		line := createLineFromWords(currentLineWords)
		lines = append(lines, line)
	}

	return lines
}

func wordsOnSameLine(currentLineWords []WordBox, newWord WordBox) bool {
	if len(currentLineWords) == 0 {
		return true
	}

	avgHeight := 0
	minY, maxY := currentLineWords[0].Y, currentLineWords[0].Y+currentLineWords[0].Height
	for _, word := range currentLineWords {
		avgHeight += word.Height
		if word.Y < minY {
			minY = word.Y
		}
		if word.Y+word.Height > maxY {
			maxY = word.Y + word.Height
		}
	}
	avgHeight /= len(currentLineWords)

	tolerance := avgHeight / 3
	currentLineBottom := maxY + tolerance
	currentLineTop := minY - tolerance

	return newWord.Y+newWord.Height >= currentLineTop && newWord.Y <= currentLineBottom
}

func createLineFromWords(words []WordBox) LineBox {
	if len(words) == 0 {
		return LineBox{}
	}

	minX, minY := words[0].X, words[0].Y
	maxX, maxY := words[0].X+words[0].Width, words[0].Y+words[0].Height

	for _, word := range words[1:] {
		if word.X < minX {
			minX = word.X
		}
		if word.Y < minY {
			minY = word.Y
		}
		if word.X+word.Width > maxX {
			maxX = word.X + word.Width
		}
		if word.Y+word.Height > maxY {
			maxY = word.Y + word.Height
		}
	}

	return LineBox{
		Words:  words,
		X:      minX,
		Y:      minY,
		Width:  maxX - minX,
		Height: maxY - minY,
	}
}

func convertWordsAndLinesToOCRResponse(lines []LineBox, width, height int) OCRResponse {
	var paragraphs []Paragraph

	for i, line := range lines {
		word := Word{
			BoundingBox: BoundingPoly{
				Vertices: []Vertex{
					{X: line.X, Y: line.Y},
					{X: line.X + line.Width, Y: line.Y},
					{X: line.X + line.Width, Y: line.Y + line.Height},
					{X: line.X, Y: line.Y + line.Height},
				},
			},
			Symbols: []Symbol{
				{
					BoundingBox: BoundingPoly{
						Vertices: []Vertex{
							{X: line.X, Y: line.Y},
							{X: line.X + line.Width, Y: line.Y},
							{X: line.X + line.Width, Y: line.Y + line.Height},
							{X: line.X, Y: line.Y + line.Height},
						},
					},
					Text: fmt.Sprintf("line_%d", i+1),
				},
			},
		}

		paragraph := Paragraph{
			BoundingBox: BoundingPoly{
				Vertices: []Vertex{
					{X: line.X, Y: line.Y},
					{X: line.X + line.Width, Y: line.Y},
					{X: line.X + line.Width, Y: line.Y + line.Height},
					{X: line.X, Y: line.Y + line.Height},
				},
			},
			Words: []Word{word},
		}
		paragraphs = append(paragraphs, paragraph)
	}

	block := Block{
		BoundingBox: BoundingPoly{
			Vertices: []Vertex{
				{X: 0, Y: 0},
				{X: width, Y: 0},
				{X: width, Y: height},
				{X: 0, Y: height},
			},
		},
		BlockType:  "TEXT",
		Paragraphs: paragraphs,
	}

	page := Page{
		Width:  width,
		Height: height,
		Blocks: []Block{block},
	}

	return OCRResponse{
		Responses: []Response{
			{
				FullTextAnnotation: &FullTextAnnotation{
					Pages: []Page{page},
					Text:  "Custom word detection with line grouping + LLM transcription",
				},
			},
		},
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
