package cmd

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	vision "cloud.google.com/go/vision/apiv1"
	"cloud.google.com/go/vision/v2/apiv1/visionpb"
	"github.com/spf13/cobra"
)

var (
	daemonPort string
	daemonHost string
	gcpKeyPath string
)

type CorrectionSession struct {
	ID        string       `json:"id"`
	Images    []ImageItem  `json:"images"`
	Current   int          `json:"current"`
	Results   []EvalResult `json:"results"`
	Config    EvalConfig   `json:"config"`
	CreatedAt time.Time    `json:"created_at"`
}

type ImageItem struct {
	ID            string `json:"id"`
	ImagePath     string `json:"image_path"`
	ImageURL      string `json:"image_url"`
	OriginalHOCR  string `json:"original_hocr"`
	CorrectedHOCR string `json:"corrected_hocr"`
	GroundTruth   string `json:"ground_truth"`
	Completed     bool   `json:"completed"`
	ImageWidth    int    `json:"image_width"`
	ImageHeight   int    `json:"image_height"`
}

type HOCRWord struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	BBox       BBox    `json:"bbox"` // [x1, y1, x2, y2]
	Confidence float64 `json:"confidence"`
	LineID     string  `json:"line_id"`
}

// Google Cloud Vision structures
type GCVResponse struct {
	Responses []Response `json:"responses"`
}

type Response struct {
	TextAnnotations    []TextAnnotation    `json:"textAnnotations"`
	FullTextAnnotation *FullTextAnnotation `json:"fullTextAnnotation"`
}

type TextAnnotation struct {
	Locale       string       `json:"locale"`
	Description  string       `json:"description"`
	BoundingPoly BoundingPoly `json:"boundingPoly"`
}

type FullTextAnnotation struct {
	Pages []Page `json:"pages"`
	Text  string `json:"text"`
}

type Page struct {
	Property *Property `json:"property"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Blocks   []Block   `json:"blocks"`
}

type Block struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Paragraphs  []Paragraph  `json:"paragraphs"`
	BlockType   string       `json:"blockType"`
}

type Paragraph struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Words       []Word       `json:"words"`
}

type Word struct {
	Property    *Property    `json:"property"`
	BoundingBox BoundingPoly `json:"boundingBox"`
	Symbols     []Symbol     `json:"symbols"`
}

type Symbol struct {
	Property    *Property    `json:"property"`
	BoundingBox BoundingPoly `json:"boundingBox"`
	Text        string       `json:"text"`
}

type Property struct {
	DetectedLanguages []DetectedLanguage `json:"detectedLanguages"`
	DetectedBreak     *DetectedBreak     `json:"detectedBreak"`
}

type DetectedLanguage struct {
	LanguageCode string  `json:"languageCode"`
	Confidence   float64 `json:"confidence"`
}

type DetectedBreak struct {
	Type string `json:"type"`
}

type BoundingPoly struct {
	Vertices []Vertex `json:"vertices"`
}

type Vertex struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// hOCR converter
type HOCRConverter struct {
	pageCounter      int
	blockCounter     int
	paragraphCounter int
	lineCounter      int
	wordCounter      int
}

func NewHOCRConverter() *HOCRConverter {
	return &HOCRConverter{
		pageCounter:      1,
		blockCounter:     1,
		paragraphCounter: 1,
		lineCounter:      1,
		wordCounter:      1,
	}
}

var sessions = make(map[string]*CorrectionSession)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Start hOCR editor web interface",
	Long:  "Start a web server with an advanced hOCR editor that overlays text boxes on images",
	RunE:  runDaemon,
}

func init() {
	RootCmd.AddCommand(uiCmd)
	uiCmd.Flags().StringVar(&daemonPort, "port", "8888", "Port to run the web server on")
	uiCmd.Flags().StringVar(&daemonHost, "host", "localhost", "Host to bind the web server to")
	uiCmd.Flags().StringVar(&gcpKeyPath, "gcp-key", "", "Path to Google Cloud service account key file")
}

func runDaemon(cmd *cobra.Command, args []string) error {
	slog.Info("Starting HTR hOCR Editor daemon", "host", daemonHost, "port", daemonPort)

	// Set up GCP authentication if key path provided
	if gcpKeyPath != "" {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", gcpKeyPath)
		slog.Info("Using GCP service account key", "path", gcpKeyPath)
	}

	// Set up routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/sessions", handleSessions)
	http.HandleFunc("/api/sessions/", handleSessionDetail)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/hocr/parse", handleHOCRParse)
	http.HandleFunc("/api/hocr/update", handleHOCRUpdate)
	http.HandleFunc("/static/", handleStatic)

	addr := fmt.Sprintf("%s:%s", daemonHost, daemonPort)
	slog.Info("hOCR Editor interface available", "url", fmt.Sprintf("http://%s", addr))

	return http.ListenAndServe(addr, nil)
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		sessionList := make([]*CorrectionSession, 0, len(sessions))
		for _, session := range sessions {
			sessionList = append(sessionList, session)
		}
		json.NewEncoder(w).Encode(sessionList)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")

	// Handle metrics endpoint
	if strings.HasSuffix(sessionID, "/metrics") {
		sessionID = strings.TrimSuffix(sessionID, "/metrics")
		if r.Method == "POST" {
			handleMetrics(w, r, sessionID)
			return
		}
	}

	session, exists := sessions[sessionID]
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(session)
	case "PUT":
		var updatedSession CorrectionSession
		if err := json.NewDecoder(r.Body).Decode(&updatedSession); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		sessions[sessionID] = &updatedSession

		// Save corrected results to YAML file
		saveSessionResults(&updatedSession)

		json.NewEncoder(w).Encode(updatedSession)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleMetrics(w http.ResponseWriter, r *http.Request, sessionID string) {
	var request struct {
		Original  string `json:"original"`
		Corrected string `json:"corrected"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Use existing CalculateAccuracyMetrics function
	metrics := CalculateAccuracyMetrics(request.Original, request.Corrected)
	json.NewEncoder(w).Encode(metrics)
}

func handleHOCRUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		SessionID string `json:"session_id"`
		ImageID   string `json:"image_id"`
		HOCR      string `json:"hocr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	session, exists := sessions[request.SessionID]
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Find and update the image
	for i, image := range session.Images {
		if image.ID == request.ImageID {
			session.Images[i].CorrectedHOCR = request.HOCR
			session.Images[i].Completed = true
			break
		}
	}

	sessions[request.SessionID] = session

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	file, header, err := r.FormFile("files")
	if err != nil {
		// Try single file upload as fallback
		file, header, err = r.FormFile("file")
		if err != nil {
			respondWithError(w, "Failed to read file: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	defer file.Close()

	sessionID := fmt.Sprintf("session_%d", time.Now().Unix())
	session := &CorrectionSession{
		ID:        sessionID,
		Images:    []ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: EvalConfig{
			Model:       "google_cloud_vision",
			Prompt:      "Google Cloud Vision OCR with hOCR conversion",
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
	}

	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		respondWithError(w, "Failed to create uploads directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle single file upload
	filename := fmt.Sprintf("%s_%s", sessionID, header.Filename)
	filePath := filepath.Join(uploadsDir, filename)

	outFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

	// Copy uploaded file
	_, err = outFile.ReadFrom(file)
	if err != nil {
		respondWithError(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get image dimensions for hOCR scaling
	width, height := getImageDimensions(filePath)

	// Get hOCR for the image using Google Cloud Vision
	hocrXML, err := getOCRForImageGCV(filePath)
	if err != nil {
		slog.Warn("Failed to get hOCR from Google Cloud Vision", "error", err)
		http.Error(w, "Failed dependency", http.StatusInternalServerError)
	}

	imageItem := ImageItem{
		ID:            "img_1",
		ImagePath:     filename,
		ImageURL:      "/static/uploads/" + filename,
		OriginalHOCR:  hocrXML,
		CorrectedHOCR: "",
		Completed:     false,
		ImageWidth:    width,
		ImageHeight:   height,
	}

	session.Images = []ImageItem{imageItem}
	sessions[sessionID] = session

	response := map[string]interface{}{
		"session_id": sessionID,
		"message":    "Successfully processed 1 file",
		"images":     1,
	}

	json.NewEncoder(w).Encode(response)
}

func getOCRForImageGCV(imagePath string) (string, error) {
	ctx := context.Background()

	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		return "", err
	}

	f, err := os.Open(imagePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	image, err := vision.NewImageFromReader(f)
	if err != nil {
		return "", err
	}
	annotation, err := client.DetectDocumentText(ctx, image, nil)
	if err != nil {
		return "", err
	}
	// Convert the Vision API response to our internal format
	gcvResponse := convertVisionResponseToGCV(annotation)

	// Convert to hOCR using our converter
	converter := NewHOCRConverter()
	hocr, err := converter.ConvertToHOCR(gcvResponse)
	if err != nil {
		return "", fmt.Errorf("failed to convert to hOCR: %w", err)
	}

	return hocr, nil
}

func convertVisionResponseToGCV(annotation *visionpb.TextAnnotation) GCVResponse {
	if annotation == nil {
		return GCVResponse{}
	}

	var pages []Page
	for _, page := range annotation.Pages {
		convertedPage := Page{
			Width:  int(page.Width),
			Height: int(page.Height),
		}

		// Convert blocks
		for _, block := range page.Blocks {
			convertedBlock := Block{
				BoundingBox: convertBoundingPoly(block.BoundingBox),
				BlockType:   "TEXT",
			}

			// Convert paragraphs
			for _, paragraph := range block.Paragraphs {
				convertedParagraph := Paragraph{
					BoundingBox: convertBoundingPoly(paragraph.BoundingBox),
				}

				// Convert words
				for _, word := range paragraph.Words {
					convertedWord := Word{
						BoundingBox: convertBoundingPoly(word.BoundingBox),
					}

					// Convert symbols
					for _, symbol := range word.Symbols {
						convertedSymbol := Symbol{
							BoundingBox: convertBoundingPoly(symbol.BoundingBox),
							Text:        symbol.Text,
						}

						// Convert break info
						if symbol.Property != nil && symbol.Property.DetectedBreak != nil {
							convertedSymbol.Property = &Property{
								DetectedBreak: &DetectedBreak{
									Type: symbol.Property.DetectedBreak.Type.String(),
								},
							}
						}

						convertedWord.Symbols = append(convertedWord.Symbols, convertedSymbol)
					}

					convertedParagraph.Words = append(convertedParagraph.Words, convertedWord)
				}

				convertedBlock.Paragraphs = append(convertedBlock.Paragraphs, convertedParagraph)
			}

			convertedPage.Blocks = append(convertedPage.Blocks, convertedBlock)
		}

		pages = append(pages, convertedPage)
	}

	return GCVResponse{
		Responses: []Response{
			{
				FullTextAnnotation: &FullTextAnnotation{
					Pages: pages,
					Text:  annotation.Text,
				},
			},
		},
	}
}

func convertBoundingPoly(poly *visionpb.BoundingPoly) BoundingPoly {
	if poly == nil {
		return BoundingPoly{}
	}

	var vertices []Vertex
	for _, vertex := range poly.Vertices {
		vertices = append(vertices, Vertex{
			X: int(vertex.X),
			Y: int(vertex.Y),
		})
	}

	return BoundingPoly{Vertices: vertices}
}

// hOCR Converter methods (same as before)
func (h *HOCRConverter) ConvertToHOCR(gcvResponse GCVResponse) (string, error) {
	if len(gcvResponse.Responses) == 0 {
		return "", fmt.Errorf("no responses found in GCV data")
	}

	response := gcvResponse.Responses[0]
	if response.FullTextAnnotation == nil {
		return "", fmt.Errorf("no full text annotation found")
	}

	var hocr strings.Builder

	// hOCR header
	hocr.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	hocr.WriteString("<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\"\n")
	hocr.WriteString("    \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd\">\n")
	hocr.WriteString("<html xmlns=\"http://www.w3.org/1999/xhtml\" xml:lang=\"en\" lang=\"en\">\n")
	hocr.WriteString("<head>\n")
	hocr.WriteString("<title></title>\n")
	hocr.WriteString("<meta http-equiv=\"Content-Type\" content=\"text/html; charset=utf-8\" />\n")
	hocr.WriteString("<meta name='ocr-system' content='google-cloud-vision' />\n")
	hocr.WriteString("<meta name='ocr-capabilities' content='ocr_page ocr_carea ocr_par ocr_line ocrx_word' />\n")
	hocr.WriteString("</head>\n")
	hocr.WriteString("<body>\n")

	// Process pages
	for _, page := range response.FullTextAnnotation.Pages {
		pageHOCR := h.convertPage(page)
		hocr.WriteString(pageHOCR)
	}

	hocr.WriteString("</body>\n")
	hocr.WriteString("</html>\n")

	return hocr.String(), nil
}

func (h *HOCRConverter) convertPage(page Page) string {
	bbox := fmt.Sprintf("bbox 0 0 %d %d", page.Width, page.Height)

	var pageBuilder strings.Builder
	pageBuilder.WriteString(fmt.Sprintf("<div class='ocr_page' id='page_%d' title='%s'>\n",
		h.pageCounter, bbox))

	// Process blocks
	for _, block := range page.Blocks {
		if block.BlockType == "TEXT" {
			blockHOCR := h.convertBlock(block)
			pageBuilder.WriteString(blockHOCR)
		}
	}

	pageBuilder.WriteString("</div>\n")
	h.pageCounter++
	return pageBuilder.String()
}

func (h *HOCRConverter) convertBlock(block Block) string {
	bbox := h.boundingPolyToBBox(block.BoundingBox)

	var blockBuilder strings.Builder
	blockBuilder.WriteString(fmt.Sprintf("<div class='ocr_carea' id='carea_%d' title='%s'>\n",
		h.blockCounter, bbox))

	// Process paragraphs
	for _, paragraph := range block.Paragraphs {
		paragraphHOCR := h.convertParagraph(paragraph)
		blockBuilder.WriteString(paragraphHOCR)
	}

	blockBuilder.WriteString("</div>\n")
	h.blockCounter++
	return blockBuilder.String()
}

func (h *HOCRConverter) convertParagraph(paragraph Paragraph) string {
	bbox := h.boundingPolyToBBox(paragraph.BoundingBox)

	var paragraphBuilder strings.Builder
	paragraphBuilder.WriteString(fmt.Sprintf("<p class='ocr_par' id='par_%d' title='%s'>\n",
		h.paragraphCounter, bbox))

	// Group words into lines based on their vertical position and line breaks
	lines := h.groupWordsIntoLines(paragraph.Words)

	for _, line := range lines {
		lineHOCR := h.convertLine(line)
		paragraphBuilder.WriteString(lineHOCR)
	}

	paragraphBuilder.WriteString("</p>\n")
	h.paragraphCounter++
	return paragraphBuilder.String()
}

func (h *HOCRConverter) groupWordsIntoLines(words []Word) [][]Word {
	if len(words) == 0 {
		return nil
	}

	var lines [][]Word
	var currentLine []Word

	for i, word := range words {
		currentLine = append(currentLine, word)

		// Check if this word ends a line
		shouldEndLine := false

		// Check the last symbol of the word for line break
		if len(word.Symbols) > 0 {
			lastSymbol := word.Symbols[len(word.Symbols)-1]
			if lastSymbol.Property != nil && lastSymbol.Property.DetectedBreak != nil {
				breakType := lastSymbol.Property.DetectedBreak.Type
				if breakType == "LINE_BREAK" || breakType == "EOL_SURE_SPACE" {
					shouldEndLine = true
				}
			}
		}

		// Also end line if this is the last word
		if i == len(words)-1 {
			shouldEndLine = true
		}

		if shouldEndLine {
			lines = append(lines, currentLine)
			currentLine = nil
		}
	}

	return lines
}

func (h *HOCRConverter) convertLine(words []Word) string {
	if len(words) == 0 {
		return ""
	}

	// Calculate bounding box for the entire line
	lineBBox := h.calculateLineBoundingBox(words)

	var lineBuilder strings.Builder
	lineBuilder.WriteString(fmt.Sprintf("<span class='ocr_line' id='line_%d' title='%s'>",
		h.lineCounter, lineBBox))

	// Process words in the line
	for _, word := range words {
		wordHOCR := h.convertWord(word)
		lineBuilder.WriteString(wordHOCR)
	}

	lineBuilder.WriteString("</span>\n")
	h.lineCounter++
	return lineBuilder.String()
}

func (h *HOCRConverter) convertWord(word Word) string {
	bbox := h.boundingPolyToBBox(word.BoundingBox)

	// Extract text from symbols
	var text strings.Builder
	for _, symbol := range word.Symbols {
		text.WriteString(symbol.Text)
	}

	// Get confidence (default to 95 for GCV since it doesn't provide word-level confidence)
	confidence := "; x_wconf 95"

	// Get language information
	lang := ""
	if word.Property != nil && len(word.Property.DetectedLanguages) > 0 {
		lang = fmt.Sprintf("; x_lang %s", word.Property.DetectedLanguages[0].LanguageCode)
	}

	title := bbox + confidence + lang

	wordHOCR := fmt.Sprintf("<span class='ocrx_word' id='word_%d' title='%s'>%s</span>",
		h.wordCounter, title, html.EscapeString(text.String()))

	// Add space after word if not at end of line
	if len(word.Symbols) > 0 {
		lastSymbol := word.Symbols[len(word.Symbols)-1]
		if lastSymbol.Property == nil || lastSymbol.Property.DetectedBreak == nil ||
			(lastSymbol.Property.DetectedBreak.Type != "LINE_BREAK" &&
				lastSymbol.Property.DetectedBreak.Type != "EOL_SURE_SPACE") {
			wordHOCR += " "
		}
	}

	h.wordCounter++
	return wordHOCR
}

func (h *HOCRConverter) calculateLineBoundingBox(words []Word) string {
	if len(words) == 0 {
		return "bbox 0 0 0 0"
	}

	minX, minY := int(^uint(0)>>1), int(^uint(0)>>1) // max int
	maxX, maxY := 0, 0

	for _, word := range words {
		for _, vertex := range word.BoundingBox.Vertices {
			if vertex.X < minX {
				minX = vertex.X
			}
			if vertex.X > maxX {
				maxX = vertex.X
			}
			if vertex.Y < minY {
				minY = vertex.Y
			}
			if vertex.Y > maxY {
				maxY = vertex.Y
			}
		}
	}

	return fmt.Sprintf("bbox %d %d %d %d", minX, minY, maxX, maxY)
}

func (h *HOCRConverter) boundingPolyToBBox(boundingPoly BoundingPoly) string {
	if len(boundingPoly.Vertices) == 0 {
		return "bbox 0 0 0 0"
	}

	minX, minY := int(^uint(0)>>1), int(^uint(0)>>1) // max int
	maxX, maxY := 0, 0

	for _, vertex := range boundingPoly.Vertices {
		if vertex.X < minX {
			minX = vertex.X
		}
		if vertex.X > maxX {
			maxX = vertex.X
		}
		if vertex.Y < minY {
			minY = vertex.Y
		}
		if vertex.Y > maxY {
			maxY = vertex.Y
		}
	}

	return fmt.Sprintf("bbox %d %d %d %d", minX, minY, maxX, maxY)
}

// Remaining helper functions (unchanged from original)
func respondWithError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(statusCode)
	response := map[string]string{
		"error": message,
	}
	json.NewEncoder(w).Encode(response)
}

func getImageDimensions(imagePath string) (int, int) {
	// Use imagemagick identify command to get actual dimensions
	cmd := exec.Command("identify", "-format", "%w %h", imagePath)
	output, err := cmd.Output()
	if err != nil {
		slog.Warn("Failed to get image dimensions", "error", err)
		return 1000, 1400 // fallback
	}

	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) >= 2 {
		if width, err := strconv.Atoi(parts[0]); err == nil {
			if height, err := strconv.Atoi(parts[1]); err == nil {
				return width, height
			}
		}
	}

	return 1000, 1400 // fallback
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	filepath := strings.TrimPrefix(r.URL.Path, "/static/")
	http.ServeFile(w, r, filepath)
}

func saveSessionResults(session *CorrectionSession) error {
	return nil
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Serve static/index.html
	http.ServeFile(w, r, "static/index.html")
}

func handleHOCRParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		HOCR string `json:"hocr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	words, err := parseHOCRWords(request.HOCR)
	if err != nil {
		http.Error(w, "Failed to parse hOCR", http.StatusBadRequest)
		return
	}

	response := struct {
		Words []HOCRWord `json:"words"`
	}{
		Words: words,
	}

	json.NewEncoder(w).Encode(response)
}

// BBox represents bounding box coordinates
type BBox struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

// XMLElement represents any XML element during parsing
type XMLElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr   `xml:",any,attr"`
	Content  string       `xml:",chardata"`
	Children []XMLElement `xml:",any"`
}

func parseHOCRWords(hocrXML string) ([]HOCRWord, error) {
	var doc XMLElement

	// Parse the XML
	decoder := xml.NewDecoder(strings.NewReader(hocrXML))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	var words []HOCRWord

	// Recursively traverse the XML tree to find word elements
	traverseElements(doc, &words)

	return words, nil
}

func traverseElements(element XMLElement, words *[]HOCRWord) {
	// Check if this element is an ocrx_word
	if isWordElement(element) {
		word, err := parseWordElement(element)
		if err == nil && word.ID != "" {
			*words = append(*words, word)
		}
	}

	// Recursively check children
	for _, child := range element.Children {
		traverseElements(child, words)
	}
}

func isWordElement(element XMLElement) bool {
	for _, attr := range element.Attrs {
		if attr.Name.Local == "class" && strings.Contains(attr.Value, "ocrx_word") {
			return true
		}
	}
	return false
}

func parseWordElement(element XMLElement) (HOCRWord, error) {
	word := HOCRWord{}

	// Extract ID
	for _, attr := range element.Attrs {
		switch attr.Name.Local {
		case "id":
			word.ID = attr.Value
		case "title":
			// Parse title attribute for bbox and confidence
			if err := parseTitleAttribute(attr.Value, &word); err != nil {
				return word, fmt.Errorf("failed to parse title attribute: %w", err)
			}
		}
	}

	// Extract text content
	word.Text = strings.TrimSpace(element.Content)

	return word, nil
}

func parseTitleAttribute(title string, word *HOCRWord) error {
	// Parse bbox coordinates
	bboxRegex := regexp.MustCompile(`bbox\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)`)
	if matches := bboxRegex.FindStringSubmatch(title); len(matches) == 5 {
		var err error
		if word.BBox.X1, err = strconv.Atoi(matches[1]); err != nil {
			return fmt.Errorf("invalid bbox x1: %w", err)
		}
		if word.BBox.Y1, err = strconv.Atoi(matches[2]); err != nil {
			return fmt.Errorf("invalid bbox y1: %w", err)
		}
		if word.BBox.X2, err = strconv.Atoi(matches[3]); err != nil {
			return fmt.Errorf("invalid bbox x2: %w", err)
		}
		if word.BBox.Y2, err = strconv.Atoi(matches[4]); err != nil {
			return fmt.Errorf("invalid bbox y2: %w", err)
		}
	}

	// Parse confidence (x_wconf)
	confRegex := regexp.MustCompile(`x_wconf\s+(\d+(?:\.\d+)?)`)
	if matches := confRegex.FindStringSubmatch(title); len(matches) == 2 {
		var err error
		if word.Confidence, err = strconv.ParseFloat(matches[1], 64); err != nil {
			return fmt.Errorf("invalid confidence: %w", err)
		}
	}

	return nil
}

func ensureUniqueWordIDs(hocr string) string {
	// Simple regex to find and fix duplicate or missing word IDs
	re := regexp.MustCompile(`<span class=['"]ocrx_word['"]([^>]*?)>`)
	wordCount := 0

	result := re.ReplaceAllStringFunc(hocr, func(match string) string {
		wordCount++

		// Check if ID already exists
		if strings.Contains(match, `id=`) {
			return match // Keep existing ID
		}

		// Add missing ID
		idAttr := fmt.Sprintf(` id="word_1_1_%d"`, wordCount)
		return strings.Replace(match, `>`, idAttr+`>`, 1)
	})

	return result
}
