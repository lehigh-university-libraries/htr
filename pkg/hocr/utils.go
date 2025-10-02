package hocr

import (
	"fmt"
	"strings"
)

// ConvertToBasicHOCR converts an OCR response to basic hOCR without LLM transcription
func ConvertToBasicHOCR(response OCRResponse) string {
	var lines []string

	if len(response.Responses) == 0 || response.Responses[0].FullTextAnnotation == nil {
		return WrapInHOCRDocument("")
	}

	wordIndex := 0
	for _, page := range response.Responses[0].FullTextAnnotation.Pages {
		for _, block := range page.Blocks {
			for _, paragraph := range block.Paragraphs {
				for _, word := range paragraph.Words {
					if len(word.BoundingBox.Vertices) >= 4 && len(word.Symbols) > 0 {
						bbox := word.BoundingBox
						text := word.Symbols[0].Text
						line := fmt.Sprintf(`<span class='ocrx_line' id='line_%d' title='bbox %d %d %d %d'><span class='ocrx_word' id='word_%d' title='bbox %d %d %d %d'>%s</span></span>`,
							wordIndex+1,
							bbox.Vertices[0].X, bbox.Vertices[0].Y,
							bbox.Vertices[2].X, bbox.Vertices[2].Y,
							wordIndex+1,
							bbox.Vertices[0].X, bbox.Vertices[0].Y,
							bbox.Vertices[2].X, bbox.Vertices[2].Y,
							text)
						lines = append(lines, line)
						wordIndex++
					}
				}
			}
		}
	}

	return WrapInHOCRDocument(strings.Join(lines, "\n"))
}

// WrapInHOCRDocument wraps content in a complete hOCR HTML document
func WrapInHOCRDocument(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head>
<title></title>
<meta http-equiv="Content-Type" content="text/html;charset=utf-8" />
<meta name='ocr-system' content='htr' />
</head>
<body>
<div class='ocr_page' id='page_1'>
%s
</div>
</body>
</html>`, content)
}

// CleanProviderResponse cleans up provider response for XML compatibility
func CleanProviderResponse(content string) string {
	result := content
	result = fixAmpersands(result)
	result = escapeTextContent(result)
	return result
}

func fixAmpersands(content string) string {
	validEntities := []string{"&amp;", "&lt;", "&gt;", "&quot;", "&apos;", "&#39;"}

	result := content
	lines := strings.Split(result, "\n")
	var cleanLines []string

	for _, line := range lines {
		cleanLine := line

		for i := 0; i < len(cleanLine); i++ {
			if cleanLine[i] == '&' {
				isValidEntity := false
				for _, entity := range validEntities {
					if i+len(entity) <= len(cleanLine) && cleanLine[i:i+len(entity)] == entity {
						isValidEntity = true
						i += len(entity) - 1
						break
					}
				}

				if !isValidEntity && i+2 < len(cleanLine) && cleanLine[i+1] == '#' {
					j := i + 2
					for j < len(cleanLine) && cleanLine[j] >= '0' && cleanLine[j] <= '9' {
						j++
					}
					if j < len(cleanLine) && cleanLine[j] == ';' {
						isValidEntity = true
						i = j
					}
				}

				if !isValidEntity {
					cleanLine = cleanLine[:i] + "&amp;" + cleanLine[i+1:]
					i += 4
				}
			}
		}

		cleanLines = append(cleanLines, cleanLine)
	}

	return strings.Join(cleanLines, "\n")
}

func escapeTextContent(content string) string {
	lines := strings.Split(content, "\n")
	var cleanLines []string

	for _, line := range lines {
		if strings.Contains(line, "<span") && strings.Contains(line, "</span>") {
			cleaned := escapeTextInSpans(line)
			cleanLines = append(cleanLines, cleaned)
		} else {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}

func escapeTextInSpans(line string) string {
	parts := strings.Split(line, "</span>")

	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		lastGT := strings.LastIndex(part, ">")
		if lastGT >= 0 && lastGT < len(part)-1 {
			before := part[:lastGT+1]
			text := part[lastGT+1:]

			text = strings.ReplaceAll(text, "<", "&lt;")
			text = strings.ReplaceAll(text, ">", "&gt;")

			parts[i] = before + text
		}
	}

	return strings.Join(parts, "</span>")
}
