package hocr

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// WordImage represents an extracted word with its image data and metadata
type WordImage struct {
	Index       int
	BoundingBox BoundingPoly
	ImagePath   string
	Text        string // Will be filled by transcription
}

// TranscribeWordsIndividually extracts individual word images and transcribes each one
func TranscribeWordsIndividually(imagePath string, response OCRResponse, provider providers.Provider, config providers.Config) (string, error) {
	if len(response.Responses) == 0 || response.Responses[0].FullTextAnnotation == nil {
		return "", fmt.Errorf("no text annotation in response")
	}

	tempDir := "/tmp"
	var wordImages []WordImage
	var tempPaths []string

	// Extract individual word images grouped by lines
	wordIndex := 0
	for _, page := range response.Responses[0].FullTextAnnotation.Pages {
		for _, block := range page.Blocks {
			for _, paragraph := range block.Paragraphs {
				for _, word := range paragraph.Words {
					if len(word.BoundingBox.Vertices) < 4 {
						continue
					}

					// Extract word image
					wordImagePath, err := ExtractWordImage(imagePath, word.BoundingBox, tempDir, wordIndex)
					if err != nil {
						slog.Warn("Failed to extract word image", "wordIndex", wordIndex, "error", err)
						continue
					}

					wordImages = append(wordImages, WordImage{
						Index:       wordIndex,
						BoundingBox: word.BoundingBox,
						ImagePath:   wordImagePath,
					})
					tempPaths = append(tempPaths, wordImagePath)
					wordIndex++
				}
			}
		}
	}

	// Clean up extracted images when done
	defer func() {
		for _, path := range tempPaths {
			os.Remove(path)
		}
	}()

	// Group words into lines for better context
	lineGroups := groupWordsByLines(wordImages)

	// Transcribe line by line for better context
	for _, lineWords := range lineGroups {
		if len(lineWords) == 1 {
			// Single word - transcribe individually
			text, err := transcribeWordImage(lineWords[0].ImagePath, provider, config)
			if err != nil {
				slog.Warn("Failed to transcribe word", "wordIndex", lineWords[0].Index, "error", err)
				lineWords[0].Text = ""
			} else {
				lineWords[0].Text = strings.TrimSpace(text)
			}
		} else {
			// Multiple words on same line - transcribe together for context
			lineText, err := transcribeLineImage(imagePath, lineWords, provider, config, tempDir)
			if err != nil {
				slog.Warn("Failed to transcribe line", "wordCount", len(lineWords), "error", err)
				// Fall back to individual word transcription
				for _, word := range lineWords {
					text, err := transcribeWordImage(word.ImagePath, provider, config)
					if err != nil {
						word.Text = ""
					} else {
						word.Text = strings.TrimSpace(text)
					}
				}
			} else {
				// Distribute the line text across words
				distributeLineTextToWords(lineText, lineWords)
			}
		}
	}

	// Build hOCR XML from transcribed words
	return buildHOCRFromWords(wordImages), nil
}

// transcribeWordImage sends a single word image to the LLM for transcription
func transcribeWordImage(imagePath string, provider providers.Provider, config providers.Config) (string, error) {
	// Read and encode image
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read word image: %w", err)
	}
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// Create a focused OCR prompt for individual words
	wordConfig := config
	wordConfig.Prompt = `You are an OCR (Optical Character Recognition) system. Your task is to extract and transcribe the text from this image.

INSTRUCTIONS:
- Look carefully at the image and identify any text, letters, numbers, or symbols
- Return ONLY the text content you can read, nothing else
- Do not add explanations, descriptions, or apologies
- If the text is handwritten, do your best to interpret it
- If you cannot read anything clearly, return just a single space character
- Preserve capitalization and punctuation as you see it

TEXT:`

	// Extract text using the provider with retry
	var result string
	var lastErr error
	maxRetries := 2

	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, _, lastErr = provider.ExtractText(context.Background(), wordConfig, imagePath, imageBase64)
		if lastErr == nil {
			// Clean up common OCR response issues
			result = strings.TrimSpace(result)
			if result != "" && !strings.Contains(strings.ToLower(result), "sorry") && !strings.Contains(strings.ToLower(result), "can't") {
				return result, nil
			}
		}

		if attempt < maxRetries {
			// Slightly modify prompt for retry
			wordConfig.Prompt = `This is an OCR task. Extract any visible text from this image. Return only the text characters you can see, even if unclear. Do not apologize or explain.`
		}
	}

	// If all retries failed or returned apologetic responses, return empty
	return "", lastErr
}

// buildHOCRFromWords constructs hOCR XML from transcribed word images
func buildHOCRFromWords(wordImages []WordImage) string {
	var lines []string

	for _, word := range wordImages {
		if word.Text == "" {
			continue // Skip words that couldn't be transcribed
		}

		// Escape HTML entities in the text
		escapedText := CleanProviderResponse(word.Text)

		// Create hOCR line and word markup
		line := fmt.Sprintf(`<span class='ocrx_line' id='line_%d' title='bbox %d %d %d %d'><span class='ocrx_word' id='word_%d' title='bbox %d %d %d %d'>%s</span></span>`,
			word.Index+1,
			word.BoundingBox.Vertices[0].X, word.BoundingBox.Vertices[0].Y,
			word.BoundingBox.Vertices[2].X, word.BoundingBox.Vertices[2].Y,
			word.Index+1,
			word.BoundingBox.Vertices[0].X, word.BoundingBox.Vertices[0].Y,
			word.BoundingBox.Vertices[2].X, word.BoundingBox.Vertices[2].Y,
			escapedText)

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// groupWordsByLines groups words that are on the same horizontal line
func groupWordsByLines(words []WordImage) [][]*WordImage {
	if len(words) == 0 {
		return nil
	}

	// Create pointers to the original words for modification
	wordPtrs := make([]*WordImage, len(words))
	for i := range words {
		wordPtrs[i] = &words[i]
	}

	// Sort words by Y coordinate first, then X coordinate
	sort.Slice(wordPtrs, func(i, j int) bool {
		yDiff := abs(wordPtrs[i].BoundingBox.Vertices[0].Y - wordPtrs[j].BoundingBox.Vertices[0].Y)
		if yDiff < 20 { // Same line threshold - 20 pixels
			return wordPtrs[i].BoundingBox.Vertices[0].X < wordPtrs[j].BoundingBox.Vertices[0].X
		}
		return wordPtrs[i].BoundingBox.Vertices[0].Y < wordPtrs[j].BoundingBox.Vertices[0].Y
	})

	var lines [][]*WordImage
	var currentLine []*WordImage

	for _, word := range wordPtrs {
		if len(currentLine) == 0 {
			currentLine = append(currentLine, word)
		} else {
			// Check if this word is on the same line as the current line
			lastWord := currentLine[len(currentLine)-1]
			yDiff := abs(word.BoundingBox.Vertices[0].Y - lastWord.BoundingBox.Vertices[0].Y)

			if yDiff < 20 { // Same line
				currentLine = append(currentLine, word)
			} else {
				// Start new line
				lines = append(lines, currentLine)
				currentLine = []*WordImage{word}
			}
		}
	}

	// Don't forget the last line
	if len(currentLine) > 0 {
		lines = append(lines, currentLine)
	}

	return lines
}

// transcribeLineImage extracts a line image and transcribes it for better context
func transcribeLineImage(imagePath string, lineWords []*WordImage, provider providers.Provider, config providers.Config, tempDir string) (string, error) {
	// Calculate line bounding box
	minX, minY := lineWords[0].BoundingBox.Vertices[0].X, lineWords[0].BoundingBox.Vertices[0].Y
	maxX, maxY := lineWords[0].BoundingBox.Vertices[2].X, lineWords[0].BoundingBox.Vertices[2].Y

	for _, word := range lineWords[1:] {
		if word.BoundingBox.Vertices[0].X < minX {
			minX = word.BoundingBox.Vertices[0].X
		}
		if word.BoundingBox.Vertices[0].Y < minY {
			minY = word.BoundingBox.Vertices[0].Y
		}
		if word.BoundingBox.Vertices[2].X > maxX {
			maxX = word.BoundingBox.Vertices[2].X
		}
		if word.BoundingBox.Vertices[2].Y > maxY {
			maxY = word.BoundingBox.Vertices[2].Y
		}
	}

	// Create line bounding box with padding
	lineBbox := BoundingPoly{
		Vertices: []Vertex{
			{X: minX, Y: minY},
			{X: maxX, Y: minY},
			{X: maxX, Y: maxY},
			{X: minX, Y: maxY},
		},
	}

	// Extract line image
	lineImagePath, err := ExtractWordImage(imagePath, lineBbox, tempDir, 9999) // Use high index to avoid conflicts
	if err != nil {
		return "", err
	}
	defer os.Remove(lineImagePath)

	// Read and encode line image
	imageData, err := os.ReadFile(lineImagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read line image: %w", err)
	}
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// Create line transcription prompt
	lineConfig := config
	lineConfig.Prompt = `You are an OCR (Optical Character Recognition) system. Your task is to extract and transcribe the text from this line of text.

INSTRUCTIONS:
- This image contains a line of text with multiple words
- Read the text from left to right
- Return ONLY the text content you can read, with spaces between words
- Do not add explanations, descriptions, or apologies
- If the text is handwritten, do your best to interpret it
- Preserve capitalization and punctuation as you see it
- If you cannot read some words, use your best guess based on context

TEXT:`

	// Transcribe the line
	result, _, err := provider.ExtractText(context.Background(), lineConfig, lineImagePath, imageBase64)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(result), nil
}

// distributeLineTextToWords distributes transcribed line text across individual words
func distributeLineTextToWords(lineText string, lineWords []*WordImage) {
	words := strings.Fields(lineText)

	// If we have the same number of words, distribute directly
	if len(words) == len(lineWords) {
		for i, word := range words {
			lineWords[i].Text = word
		}
		return
	}

	// If we have more detected words than transcribed words, combine some
	if len(words) < len(lineWords) {
		// Distribute words as evenly as possible
		for i, word := range lineWords {
			if i < len(words) {
				word.Text = words[i]
			} else {
				word.Text = "" // Extra words get empty text
			}
		}
		return
	}

	// If we have more transcribed words than detected words, split the text
	if len(words) > len(lineWords) {
		// Join extra words and distribute across available slots
		wordsPerSlot := len(words) / len(lineWords)
		remainder := len(words) % len(lineWords)

		wordIndex := 0
		for i, word := range lineWords {
			wordsToTake := wordsPerSlot
			if i < remainder {
				wordsToTake++
			}

			var wordParts []string
			for j := 0; j < wordsToTake && wordIndex < len(words); j++ {
				wordParts = append(wordParts, words[wordIndex])
				wordIndex++
			}
			word.Text = strings.Join(wordParts, " ")
		}
	}
}

// CreateTextImage creates an image containing the specified text
func CreateTextImage(text, tempDir, filename string) (string, error) {
	outputPath := filepath.Join(tempDir, fmt.Sprintf("%s_%d.png", filename, time.Now().Unix()))

	cmd := exec.Command("magick",
		"-size", "2000x60",
		"xc:white",
		"-fill", "black",
		"-font", "DejaVu-Sans-Mono",
		"-pointsize", "24",
		"-draw", fmt.Sprintf(`text 10,40 "%s"`, text),
		outputPath)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create text image: %w", err)
	}

	return outputPath, nil
}

// ExtractWordImage extracts a word region from the source image
func ExtractWordImage(imagePath string, bbox BoundingPoly, tempDir string, wordIndex int) (string, error) {
	if len(bbox.Vertices) < 4 {
		return "", fmt.Errorf("invalid bounding box")
	}

	minX := bbox.Vertices[0].X
	minY := bbox.Vertices[0].Y
	maxX := bbox.Vertices[2].X
	maxY := bbox.Vertices[2].Y

	width := maxX - minX
	height := maxY - minY

	if width <= 0 || height <= 0 {
		return "", fmt.Errorf("invalid dimensions")
	}

	// Add larger padding for better visibility
	padding := 10
	cropX := max(0, minX-padding)
	cropY := max(0, minY-padding)
	cropWidth := width + 2*padding
	cropHeight := height + 2*padding

	outputPath := filepath.Join(tempDir, fmt.Sprintf("word_img_%d_%d.png", wordIndex, time.Now().Unix()))

	cmd := exec.Command("magick", imagePath,
		"-crop", fmt.Sprintf("%dx%d+%d+%d", cropWidth, cropHeight, cropX, cropY),
		"+repage",
		outputPath)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to extract word image: %w", err)
	}

	return outputPath, nil
}
