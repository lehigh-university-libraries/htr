package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	daemonPort string
	daemonHost string
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
	ID     string `json:"id"`
	Text   string `json:"text"`
	Bbox   []int  `json:"bbox"` // [x1, y1, x2, y2]
	Conf   int    `json:"conf"`
	LineID string `json:"line_id"`
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
}

func runDaemon(cmd *cobra.Command, args []string) error {
	slog.Info("Starting HTR hOCR Editor daemon", "host", daemonHost, "port", daemonPort)

	// Set up routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/sessions", handleSessions)
	http.HandleFunc("/api/sessions/", handleSessionDetail)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/hocr/parse", handleHOCRParse)
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
			Model:       "hocr_correction",
			Prompt:      "hOCR OCR correction session",
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

	// Get hOCR for the image
	hocrXML, err := getOCRForImage(filePath)
	if err != nil {
		slog.Warn("Failed to get hOCR", "error", err)
		hocrXML = generateBasicHOCR("Failed to extract OCR text. Please edit manually.")
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

func respondWithError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(statusCode)
	response := map[string]string{
		"error": message,
	}
	json.NewEncoder(w).Encode(response)
}

func getImageDimensions(imagePath string) (int, int) {
	// Basic image dimension detection
	// In a real implementation, you'd use an image library
	// For now, return reasonable defaults
	return 1000, 1400
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

func parseHOCRWords(hocrXML string) ([]HOCRWord, error) {
	// Basic hOCR parsing - in a real implementation, you'd use a proper XML parser
	words := []HOCRWord{}

	// This is a simplified parser - you'd want to use golang.org/x/net/html or similar
	lines := strings.Split(hocrXML, "\n")

	for _, line := range lines {
		if strings.Contains(line, "ocrx_word") {
			word := parseHOCRWordLine(line)
			if word.ID != "" {
				words = append(words, word)
			}
		}
	}

	return words, nil
}

func parseHOCRWordLine(line string) HOCRWord {
	// Extract word ID
	idStart := strings.Index(line, `id="`)
	if idStart == -1 {
		return HOCRWord{}
	}
	idStart += 4
	idEnd := strings.Index(line[idStart:], `"`)
	if idEnd == -1 {
		return HOCRWord{}
	}
	id := line[idStart : idStart+idEnd]

	// Extract bbox
	bboxStart := strings.Index(line, "bbox ")
	if bboxStart == -1 {
		return HOCRWord{}
	}
	bboxStart += 5
	bboxEnd := strings.Index(line[bboxStart:], ";")
	if bboxEnd == -1 {
		bboxEnd = strings.Index(line[bboxStart:], `"`)
	}
	if bboxEnd == -1 {
		return HOCRWord{}
	}

	bboxStr := line[bboxStart : bboxStart+bboxEnd]
	bboxParts := strings.Fields(bboxStr)
	bbox := []int{}
	for _, part := range bboxParts {
		if val, err := strconv.Atoi(part); err == nil {
			bbox = append(bbox, val)
		}
	}

	// Extract confidence
	conf := 95 // default confidence
	confStart := strings.Index(line, "x_wconf ")
	if confStart != -1 {
		confStart += 8
		confEnd := strings.Index(line[confStart:], `"`)
		if confEnd != -1 {
			if val, err := strconv.Atoi(line[confStart : confStart+confEnd]); err == nil {
				conf = val
			}
		}
	}

	// Extract text content
	textStart := strings.Index(line, ">")
	textEnd := strings.LastIndex(line, "<")
	if textStart == -1 || textEnd == -1 || textStart >= textEnd {
		return HOCRWord{}
	}
	text := line[textStart+1 : textEnd]

	// Generate line ID from word ID
	lineID := strings.Replace(id, "word_", "line_", 1)
	if strings.Contains(lineID, "_") {
		parts := strings.Split(lineID, "_")
		if len(parts) >= 3 {
			lineID = parts[0] + "_" + parts[1] + "_" + parts[2]
		}
	}

	return HOCRWord{
		ID:     id,
		Text:   text,
		Bbox:   bbox,
		Conf:   conf,
		LineID: lineID,
	}
}

func getOCRForImage(imagePath string) (string, error) {
	// Modified to request hOCR format from OpenAI
	config := EvalConfig{
		Model:       evalModel,
		Prompt:      "Extract all text from this image and return it in hOCR format with bounding boxes and confidence scores. Use proper hOCR XML structure with ocrx_word elements containing bbox and x_wconf attributes.",
		Temperature: 0.0,
	}

	imageBase64, err := getImageAsBase64(imagePath)
	if err != nil {
		return "", err
	}

	response, err := callOpenAI(config, imagePath, imageBase64)
	if err != nil {
		return "", err
	}

	// If response doesn't look like hOCR, generate a basic hOCR structure
	if !strings.Contains(response, "ocrx_word") {
		return generateBasicHOCR(response), nil
	}

	return response, nil
}

func generateBasicHOCR(text string) string {
	// Generate basic hOCR structure from plain text
	// This is a fallback when OCR doesn't return proper hOCR

	words := strings.Fields(text)
	hocr := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head><title></title></head>
<body>
<div class="ocr_page" id="page_1" title="bbox 0 0 1000 1000">
<span class="ocr_line" id="line_1_1" title="bbox 0 0 1000 100">`

	for i, word := range words {
		x1 := i * 80
		x2 := x1 + len(word)*10
		wordID := fmt.Sprintf("word_1_1_%d", i+1)

		hocr += fmt.Sprintf(`
  <span class="ocrx_word" id="%s" title="bbox %d 0 %d 50; x_wconf 80">%s</span>`,
			wordID, x1, x2, word)
	}

	hocr += `
</span>
</div>
</body>
</html>`

	return hocr
}
