// Add to cmd/daemon.go
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	ID           string `json:"id"`
	ImagePath    string `json:"image_path"`
	ImageURL     string `json:"image_url"`
	OriginalOCR  string `json:"original_ocr"`
	CorrectedOCR string `json:"corrected_ocr"`
	GroundTruth  string `json:"ground_truth"`
	Completed    bool   `json:"completed"`
}

var sessions = make(map[string]*CorrectionSession)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Start web interface for OCR correction",
	Long:  "Start a web server that allows manual correction of OCR output through a browser interface",
	RunE:  runDaemon,
}

func init() {
	RootCmd.AddCommand(uiCmd)
	uiCmd.Flags().StringVar(&daemonPort, "port", "8888", "Port to run the web server on")
	uiCmd.Flags().StringVar(&daemonHost, "host", "localhost", "Host to bind the web server to")
}

func runDaemon(cmd *cobra.Command, args []string) error {
	slog.Info("Starting HTR daemon", "host", daemonHost, "port", daemonPort)

	// Set up routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/sessions", handleSessions)
	http.HandleFunc("/api/sessions/", handleSessionDetail)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/static/", handleStatic)

	addr := fmt.Sprintf("%s:%s", daemonHost, daemonPort)
	slog.Info("HTR correction interface available", "url", fmt.Sprintf("http://%s", addr))

	return http.ListenAndServe(addr, nil)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>HTR OCR Correction Interface</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f5; }
        .container { max-width: 1400px; margin: 0 auto; padding: 20px; }
        .header { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .correction-interface { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 20px; }
        .image-panel, .text-panel { background: white; border-radius: 8px; padding: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .image-panel img { max-width: 100%; height: auto; border: 1px solid #ddd; border-radius: 4px; }
        .text-panel textarea { width: 100%; height: 400px; padding: 15px; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; font-size: 14px; resize: vertical; }
        .metrics { background: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .metrics-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; }
        .metric { text-align: center; }
        .metric-value { font-size: 24px; font-weight: bold; color: #2563eb; }
        .metric-label { font-size: 12px; color: #6b7280; text-transform: uppercase; letter-spacing: 0.05em; }
        .controls { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); display: flex; justify-content: space-between; align-items: center; }
        .btn { padding: 10px 20px; border: none; border-radius: 4px; cursor: pointer; font-weight: 500; text-decoration: none; display: inline-block; }
        .btn-primary { background: #2563eb; color: white; }
        .btn-secondary { background: #6b7280; color: white; }
        .btn-success { background: #059669; color: white; }
        .btn:hover { opacity: 0.9; }
        .progress { background: #e5e7eb; border-radius: 4px; height: 8px; margin-bottom: 10px; }
        .progress-bar { background: #2563eb; height: 100%; border-radius: 4px; transition: width 0.3s; }
        .upload-area { border: 2px dashed #d1d5db; border-radius: 8px; padding: 40px; text-align: center; margin-bottom: 20px; }
        .upload-area.dragover { border-color: #2563eb; background: #eff6ff; }
        .hidden { display: none; }
        .session-list { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>HTR OCR Correction Interface</h1>
            <p>Manual correction tool for OCR output evaluation</p>
        </div>

        <!-- Upload Section -->
        <div id="upload-section">
            <div class="upload-area" id="upload-area">
                <h3>Start New Correction Session</h3>
                <p>Upload a single image or CSV file with image URLs</p>
                <input type="file" id="file-input" accept=".jpg,.jpeg,.png,.gif,.csv" style="margin: 20px 0;">
                <br>
                <button class="btn btn-primary" onclick="handleUpload()">Upload</button>
            </div>
        </div>

        <!-- Sessions List -->
        <div class="session-list">
            <h3>Recent Sessions</h3>
            <div id="sessions-list">Loading...</div>
        </div>

        <!-- Correction Interface -->
        <div id="correction-section" class="hidden">
            <div class="controls">
                <div>
                    <span id="progress-text">Image 1 of 10</span>
                    <div class="progress">
                        <div class="progress-bar" id="progress-bar"></div>
                    </div>
                </div>
                <div>
                    <button class="btn btn-secondary" onclick="previousImage()">← Previous</button>
                    <button class="btn btn-success" onclick="saveAndNext()">Save & Next →</button>
                    <button class="btn btn-primary" onclick="finishSession()">Finish Session</button>
                </div>
            </div>

            <div class="correction-interface">
                <div class="image-panel">
                    <h3>Original Image</h3>
                    <img id="current-image" src="" alt="OCR Image">
                </div>
                <div class="text-panel">
                    <h3>OCR Text (Edit to Correct)</h3>
                    <textarea id="ocr-text" oninput="updateMetrics()" placeholder="OCR text will appear here..."></textarea>
                </div>
            </div>

            <div class="metrics">
                <h3>Real-time Accuracy Metrics</h3>
                <div class="metrics-grid">
                    <div class="metric">
                        <div class="metric-value" id="char-similarity">0.000</div>
                        <div class="metric-label">Character Similarity</div>
                    </div>
                    <div class="metric">
                        <div class="metric-value" id="word-accuracy">0.000</div>
                        <div class="metric-label">Word Accuracy</div>
                    </div>
                    <div class="metric">
                        <div class="metric-value" id="word-error-rate">0.000</div>
                        <div class="metric-label">Word Error Rate</div>
                    </div>
                    <div class="metric">
                        <div class="metric-value" id="total-edits">0</div>
                        <div class="metric-label">Total Edits</div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        let currentSession = null;
        let currentImageIndex = 0;

        // Load sessions on page load
        document.addEventListener('DOMContentLoaded', loadSessions);

        async function loadSessions() {
            try {
                const response = await fetch('/api/sessions');
                const sessions = await response.json();
                displaySessions(sessions);
            } catch (error) {
                console.error('Error loading sessions:', error);
            }
        }

        function displaySessions(sessions) {
            const container = document.getElementById('sessions-list');
            if (sessions.length === 0) {
                container.innerHTML = '<p>No sessions found. Upload an image or CSV to get started.</p>';
                return;
            }

            const html = sessions.map(session => 
                '<div style="border: 1px solid #ddd; padding: 15px; margin: 10px 0; border-radius: 4px;">' +
                '<h4>Session: ' + session.id + '</h4>' +
                '<p>Images: ' + session.images.length + ' | Completed: ' + session.images.filter(img => img.completed).length + '</p>' +
                '<p>Created: ' + new Date(session.created_at).toLocaleString() + '</p>' +
                '<button class="btn btn-primary" onclick="loadSession(\'' + session.id + '\')">Continue</button>' +
                '</div>'
            ).join('');
            container.innerHTML = html;
        }

        async function handleUpload() {
            const fileInput = document.getElementById('file-input');
            const file = fileInput.files[0];
            if (!file) {
                alert('Please select a file');
                return;
            }

            const formData = new FormData();
            formData.append('file', file);

            try {
                const response = await fetch('/api/upload', {
                    method: 'POST',
                    body: formData
                });
                const result = await response.json();
                if (result.session_id) {
                    loadSession(result.session_id);
                }
            } catch (error) {
                console.error('Upload error:', error);
                alert('Upload failed');
            }
        }

        async function loadSession(sessionId) {
            try {
                const response = await fetch('/api/sessions/' + sessionId);
                currentSession = await response.json();
                currentImageIndex = currentSession.current || 0;
                showCorrectionInterface();
                loadCurrentImage();
            } catch (error) {
                console.error('Error loading session:', error);
            }
        }

        function showCorrectionInterface() {
            document.getElementById('upload-section').classList.add('hidden');
            document.getElementById('correction-section').classList.remove('hidden');
        }

        function loadCurrentImage() {
            if (!currentSession || currentImageIndex >= currentSession.images.length) {
                finishSession();
                return;
            }

            const image = currentSession.images[currentImageIndex];
            document.getElementById('current-image').src = image.image_url || '/static/images/' + image.image_path;
            document.getElementById('ocr-text').value = image.corrected_ocr || image.original_ocr;
            
            updateProgress();
            updateMetrics();
        }

        function updateProgress() {
            const total = currentSession.images.length;
            const current = currentImageIndex + 1;
            const percentage = (current / total) * 100;
            
            document.getElementById('progress-text').textContent = 'Image ' + current + ' of ' + total;
            document.getElementById('progress-bar').style.width = percentage + '%';
        }

        async function updateMetrics() {
            const correctedText = document.getElementById('ocr-text').value;
            const originalText = currentSession.images[currentImageIndex].original_ocr;
            
            // Calculate metrics using the same logic as the Go backend
            try {
                const response = await fetch('/api/sessions/' + currentSession.id + '/metrics', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        original: originalText,
                        corrected: correctedText
                    })
                });
                const metrics = await response.json();
                
                document.getElementById('char-similarity').textContent = metrics.character_similarity.toFixed(3);
                document.getElementById('word-accuracy').textContent = metrics.word_accuracy.toFixed(3);
                document.getElementById('word-error-rate').textContent = metrics.word_error_rate.toFixed(3);
                document.getElementById('total-edits').textContent = metrics.substitutions + metrics.deletions + metrics.insertions;
            } catch (error) {
                console.error('Error calculating metrics:', error);
            }
        }

        async function saveAndNext() {
            const correctedText = document.getElementById('ocr-text').value;
            currentSession.images[currentImageIndex].corrected_ocr = correctedText;
            currentSession.images[currentImageIndex].completed = true;
            
            // Save to backend
            await saveSession();
            
            currentImageIndex++;
            loadCurrentImage();
        }

        function previousImage() {
            if (currentImageIndex > 0) {
                currentImageIndex--;
                loadCurrentImage();
            }
        }

        async function saveSession() {
            try {
                await fetch('/api/sessions/' + currentSession.id, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(currentSession)
                });
            } catch (error) {
                console.error('Error saving session:', error);
            }
        }

        async function finishSession() {
            await saveSession();
            alert('Session completed! Results have been saved.');
            location.reload();
        }
    </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, tmpl)
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

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	sessionID := fmt.Sprintf("session_%d", time.Now().Unix())
	session := &CorrectionSession{
		ID:        sessionID,
		Images:    []ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: EvalConfig{
			Model:       "manual_correction",
			Prompt:      "Manual OCR correction session",
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
	}

	if strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		// Handle CSV upload (similar to existing CSV processing)
		// TODO: Implement CSV parsing logic here
	} else {
		// Handle single image upload
		// Save uploaded file
		uploadsDir := "uploads"
		os.MkdirAll(uploadsDir, 0755)

		filename := fmt.Sprintf("%s_%s", sessionID, header.Filename)
		filepath := filepath.Join(uploadsDir, filename)

		outFile, err := os.Create(filepath)
		if err != nil {
			http.Error(w, "Failed to save file", http.StatusInternalServerError)
			return
		}
		defer outFile.Close()

		// Copy uploaded file
		if _, err := file.Seek(0, 0); err != nil {
			http.Error(w, "Failed to read file", http.StatusInternalServerError)
			return
		}

		if _, err := outFile.ReadFrom(file); err != nil {
			http.Error(w, "Failed to save file", http.StatusInternalServerError)
			return
		}

		// Get OCR for the image (you'll need to call your OCR API here)
		ocrText, err := getOCRForImage(filepath)
		if err != nil {
			slog.Warn("Failed to get OCR", "error", err)
			ocrText = "Failed to extract OCR text. Please enter manually."
		}

		session.Images = []ImageItem{
			{
				ID:          "img_1",
				ImagePath:   filename,
				ImageURL:    "/static/uploads/" + filename,
				OriginalOCR: ocrText,
				Completed:   false,
			},
		}
	}

	sessions[sessionID] = session

	response := map[string]string{"session_id": sessionID}
	json.NewEncoder(w).Encode(response)
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	filepath := strings.TrimPrefix(r.URL.Path, "/static/")
	http.ServeFile(w, r, filepath)
}

func getOCRForImage(imagePath string) (string, error) {
	// Use your existing OpenAI OCR logic here
	// This is a simplified version - you'll want to use your existing callOpenAI function

	config := EvalConfig{
		Model:       evalModel,
		Prompt:      "Extract all text from this image",
		Temperature: 0.0,
	}

	imageBase64, err := getImageAsBase64(imagePath)
	if err != nil {
		return "", err
	}

	return callOpenAI(config, imagePath, imageBase64)
}

func saveSessionResults(session *CorrectionSession) error {
	// Convert to your existing EvalSummary format
	var results []EvalResult

	for _, image := range session.Images {
		if !image.Completed {
			continue
		}

		// Calculate final metrics
		metrics := CalculateAccuracyMetrics(image.OriginalOCR, image.CorrectedOCR)

		result := EvalResult{
			Identifier:            image.ID,
			ImagePath:             image.ImagePath,
			TranscriptPath:        "", // No ground truth file for manual corrections
			Public:                false,
			OpenAIResponse:        image.CorrectedOCR,
			CharacterSimilarity:   metrics.CharacterSimilarity,
			WordSimilarity:        metrics.WordSimilarity,
			WordAccuracy:          metrics.WordAccuracy,
			WordErrorRate:         metrics.WordErrorRate,
			TotalWordsOriginal:    metrics.TotalWordsOriginal,
			TotalWordsTranscribed: metrics.TotalWordsTranscribed,
			CorrectWords:          metrics.CorrectWords,
			Substitutions:         metrics.Substitutions,
			Deletions:             metrics.Deletions,
			Insertions:            metrics.Insertions,
		}

		results = append(results, result)
	}

	summary := EvalSummary{
		Config:  session.Config,
		Results: results,
	}

	// Save using existing function
	outputPath := filepath.Join("evals", fmt.Sprintf("correction_%s.yaml", session.Config.Timestamp))
	return saveEvalResults(summary, outputPath)
}
