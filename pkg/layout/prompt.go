package layout

import "strings"

const pageXMLInstructions = `You are given an image of a document page and a PAGE XML layout produced by a segmentation model. Each TextLine and Word element marks a region of the image by its Coords, but has no transcription yet.

Transcribe the text inside each box and return the complete PAGE XML document exactly as given, with only the transcriptions added:
- Fill every Word element. Where a TextLine has no Word elements, fill the TextLine itself.
- Put each transcription in <TextEquiv><Unicode>...</Unicode></TextEquiv> inside its element, after any Coords, Baseline, or Word children.
- Keep every element, id, attribute, and coordinate unchanged. Do not add, remove, merge, split, or reorder elements.
- Transcribe diplomatically: keep the original spelling, punctuation, capitalization, and abbreviations. Do not correct or modernize the text.
- If a box contains no legible text, leave its Unicode element empty.
- Return only the XML document, with no commentary and no code fences.`

const hocrInstructions = `You are given an image of a document page and an hOCR layout produced by a segmentation model. Each ocrx_word element, and each ocr_line element without words, marks a region of the image by the bbox in its title attribute, but has no transcription yet.

Transcribe the text inside each box and return the complete hOCR document exactly as given, with only the transcriptions added:
- Put each word's transcription as the text content of its ocrx_word element. Where a line has no ocrx_word elements, put the line's transcription in the ocr_line element.
- Keep every element, id, class, and title attribute unchanged. Do not add, remove, merge, split, or reorder elements.
- Transcribe diplomatically: keep the original spelling, punctuation, capitalization, and abbreviations. Do not correct or modernize the text.
- If a box contains no legible text, leave it empty.
- Return only the hOCR document, with no commentary and no code fences.`

const transcriptInstructions = `A transcription of this page is provided below. Use it as the source of the text: distribute its words across the boxes in reading order, using the image to decide which words belong in each box. Do not drop, add, or alter words from the transcription.`

// buildPrompt assembles the instructions, optional transcript, and template.
func buildPrompt(t *template, instructions, transcript string, document []byte) string {
	if strings.TrimSpace(instructions) == "" {
		instructions = pageXMLInstructions
		if t.format == FormatHOCR {
			instructions = hocrInstructions
		}
	}
	var prompt strings.Builder
	prompt.WriteString(instructions)
	if transcript = strings.TrimSpace(transcript); transcript != "" {
		prompt.WriteString("\n\n" + transcriptInstructions + "\n\n<transcript>\n" + transcript + "\n</transcript>")
	}
	prompt.WriteString("\n\n<layout>\n")
	prompt.Write(document)
	prompt.WriteString("\n</layout>")
	return prompt.String()
}
