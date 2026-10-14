package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "azure" {
		t.Errorf("Expected name 'azure', got '%s'", p.Name())
	}
}

func TestProvider_ValidateConfig(t *testing.T) {
	p := New()

	tests := []struct {
		name          string
		endpoint      string
		apiKey        string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid config",
			endpoint:    "https://test.cognitiveservices.azure.com",
			apiKey:      "test-key",
			expectError: false,
		},
		{
			name:          "missing endpoint",
			endpoint:      "",
			apiKey:        "test-key",
			expectError:   true,
			errorContains: "AZURE_OCR_ENDPOINT",
		},
		{
			name:          "missing API key",
			endpoint:      "https://test.cognitiveservices.azure.com",
			apiKey:        "",
			expectError:   true,
			errorContains: "AZURE_OCR_API_KEY",
		},
		{
			name:          "both missing",
			endpoint:      "",
			apiKey:        "",
			expectError:   true,
			errorContains: "AZURE_OCR_ENDPOINT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			originalEndpoint := os.Getenv("AZURE_OCR_ENDPOINT")
			originalKey := os.Getenv("AZURE_OCR_API_KEY")
			defer func() {
				os.Setenv("AZURE_OCR_ENDPOINT", originalEndpoint)
				os.Setenv("AZURE_OCR_API_KEY", originalKey)
			}()

			os.Setenv("AZURE_OCR_ENDPOINT", tt.endpoint)
			os.Setenv("AZURE_OCR_API_KEY", tt.apiKey)

			config := providers.Config{
				Provider: "azure",
				Model:    "read-v3.2",
				Prompt:   "Extract text",
			}

			err := p.ValidateConfig(config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tt.expectError && err != nil && !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("Expected error to contain '%s', got: %v", tt.errorContains, err)
			}
		})
	}
}

func TestProvider_ExtractText(t *testing.T) {
	tests := []struct {
		name              string
		analyzeResponse   string
		analyzeStatus     int
		resultResponse    string
		resultStatus      int
		operationLocation string
		expectedText      string
		expectError       bool
		errorContains     string
	}{
		{
			name:          "successful extraction",
			analyzeStatus: http.StatusAccepted,
			analyzeResponse: `{
				"status": "running"
			}`,
			operationLocation: "/operations/test-id",
			resultStatus:      http.StatusOK,
			resultResponse: `{
				"status": "succeeded",
				"analyzeResult": {
					"readResults": [
						{
							"lines": [
								{
									"text": "Line 1 of text"
								},
								{
									"text": "Line 2 of text"
								}
							]
						}
					]
				}
			}`,
			expectedText: "Line 1 of text\nLine 2 of text",
			expectError:  false,
		},
		{
			name:          "v4.0 format response",
			analyzeStatus: http.StatusAccepted,
			analyzeResponse: `{
				"status": "running"
			}`,
			operationLocation: "/operations/test-id",
			resultStatus:      http.StatusOK,
			resultResponse: `{
				"status": "succeeded",
				"analyzeResult": {
					"pages": [
						{
							"lines": [
								{
									"content": "Page 1 line 1"
								},
								{
									"content": "Page 1 line 2"
								}
							]
						}
					]
				}
			}`,
			expectedText: "Page 1 line 1\nPage 1 line 2",
			expectError:  false,
		},
		{
			name:          "analyze request error",
			analyzeStatus: http.StatusBadRequest,
			analyzeResponse: `{
				"error": {
					"code": "InvalidRequest",
					"message": "Invalid image format"
				}
			}`,
			expectError:   true,
			errorContains: "invalid_request",
		},
		{
			name:          "missing operation location",
			analyzeStatus: http.StatusAccepted,
			analyzeResponse: `{
				"status": "running"
			}`,
			operationLocation: "", // No Operation-Location header
			expectError:       true,
			errorContains:     "invalid_response",
		},
		{
			name:          "operation failed",
			analyzeStatus: http.StatusAccepted,
			analyzeResponse: `{
				"status": "running"
			}`,
			operationLocation: "/operations/test-id",
			resultStatus:      http.StatusOK,
			resultResponse: `{
				"status": "failed",
				"error": {
					"message": "Analysis failed"
				}
			}`,
			expectError:   true,
			errorContains: "invalid_response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var analyzeCallCount int
			var resultCallCount int
			var serverURL string

			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify headers
				if r.Header.Get("Ocp-Apim-Subscription-Key") == "" {
					t.Error("Expected Ocp-Apim-Subscription-Key header")
				}

				if strings.Contains(r.URL.Path, "/analyze") {
					analyzeCallCount++
					// Analyze request
					if r.Method != http.MethodPost {
						t.Errorf("Expected POST request for analyze, got %s", r.Method)
					}
					if r.Header.Get("Content-Type") != "application/octet-stream" {
						t.Errorf("Expected application/octet-stream content type")
					}

					if tt.operationLocation != "" {
						w.Header().Set("Operation-Location", serverURL+tt.operationLocation)
					}
					w.WriteHeader(tt.analyzeStatus)
					if _, err := w.Write([]byte(tt.analyzeResponse)); err != nil {
						t.Errorf("Failed to write analyze response: %v", err)
					}

				} else if strings.Contains(r.URL.Path, "/operations/") {
					resultCallCount++
					// Result polling request
					if r.Method != http.MethodGet {
						t.Errorf("Expected GET request for result, got %s", r.Method)
					}

					w.WriteHeader(tt.resultStatus)
					if _, err := w.Write([]byte(tt.resultResponse)); err != nil {
						t.Errorf("Failed to write result response: %v", err)
					}
				}
			}))
			serverURL = server.URL
			defer server.Close()

			// Set environment variables to use test server
			originalEndpoint := os.Getenv("AZURE_OCR_ENDPOINT")
			originalKey := os.Getenv("AZURE_OCR_API_KEY")
			defer func() {
				os.Setenv("AZURE_OCR_ENDPOINT", originalEndpoint)
				os.Setenv("AZURE_OCR_API_KEY", originalKey)
			}()

			os.Setenv("AZURE_OCR_ENDPOINT", server.URL)
			os.Setenv("AZURE_OCR_API_KEY", "test-key")

			p := New()
			config := providers.Config{
				Provider: "azure",
				Model:    "read-v3.2",
				Prompt:   "Extract all text from this image",
			}

			// Use a timeout context to avoid long waits in failed tests
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result, _, err := p.ExtractText(ctx, config, "test.jpg", "dGVzdCBpbWFnZSBkYXRh") // "test image data" in base64

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if err != nil && tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if result != tt.expectedText {
					t.Errorf("Expected text '%s', got '%s'", tt.expectedText, result)
				}
			}

			// Verify that the analyze endpoint was called
			if analyzeCallCount == 0 && !tt.expectError {
				t.Error("Expected analyze endpoint to be called")
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]interface{}
		expected string
	}{
		{
			name: "v3.2 format",
			response: map[string]interface{}{
				"analyzeResult": map[string]interface{}{
					"readResults": []interface{}{
						map[string]interface{}{
							"lines": []interface{}{
								map[string]interface{}{
									"text": "First line",
								},
								map[string]interface{}{
									"text": "Second line",
								},
							},
						},
					},
				},
			},
			expected: "First line\nSecond line",
		},
		{
			name: "v4.0 format",
			response: map[string]interface{}{
				"analyzeResult": map[string]interface{}{
					"pages": []interface{}{
						map[string]interface{}{
							"lines": []interface{}{
								map[string]interface{}{
									"content": "Page content line 1",
								},
								map[string]interface{}{
									"content": "Page content line 2",
								},
							},
						},
					},
				},
			},
			expected: "Page content line 1\nPage content line 2",
		},
		{
			name: "empty response",
			response: map[string]interface{}{
				"analyzeResult": map[string]interface{}{},
			},
			expected: "",
		},
		{
			name:     "malformed response",
			response: map[string]interface{}{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractText(tt.response)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestDocumentIntelligenceAndLegacyRoutes(t *testing.T) {
	for _, model := range []string{DefaultModel, "read-v3.2", "gpt-4o"} {
		t.Run(model, func(t *testing.T) {
			legacy := model != DefaultModel
			polls := 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Ocp-Apim-Subscription-Key") != "test-key" {
					t.Error("missing resource key")
				}
				if r.Method == http.MethodPost {
					if legacy {
						if r.URL.Path != "/vision/v3.2/read/analyze" {
							t.Errorf("path=%s", r.URL.Path)
						}
					} else if r.URL.Path != "/documentintelligence/documentModels/prebuilt-read:analyze" || r.URL.Query().Get("api-version") != "2024-11-30" {
						t.Errorf("url=%s", r.URL)
					}
					w.Header().Set("Operation-Location", server.URL+"/results/1?api-version=2024-11-30")
					w.WriteHeader(http.StatusAccepted)
					return
				}
				polls++
				if polls == 1 {
					_, _ = w.Write([]byte(`{"status":"running"}`))
					return
				}
				if legacy {
					_, _ = w.Write([]byte(`{"status":"succeeded","analyzeResult":{"readResults":[{"lines":[{"text":"café"}]}]}}`))
				} else {
					_, _ = w.Write([]byte(`{"status":"succeeded","analyzeResult":{"content":"café"}}`))
				}
			}))
			defer server.Close()
			t.Setenv("AZURE_OCR_ENDPOINT", server.URL)
			t.Setenv("AZURE_OCR_API_KEY", "test-key")
			provider := &Provider{pollInterval: time.Millisecond}
			text, _, err := provider.ExtractText(t.Context(), providers.Config{Model: model}, "page.png", base64.StdEncoding.EncodeToString([]byte("image")))
			if err != nil {
				t.Fatal(err)
			}
			if text != "café" || polls != 2 {
				t.Fatalf("text=%q polls=%d", text, polls)
			}
		})
	}
}

func TestPollingCannotSendKeyToAnotherOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Operation-Location", "https://untrusted.invalid/result")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	t.Setenv("AZURE_OCR_ENDPOINT", server.URL)
	t.Setenv("AZURE_OCR_API_KEY", "secret-key")
	_, _, err := New().ExtractText(t.Context(), providers.Config{}, "page.png", base64.StdEncoding.EncodeToString([]byte("image")))
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) || providerErr.Kind != providers.ErrorInvalidResponse {
		t.Fatalf("error=%v", err)
	}
}

func TestPollingRespectsCancellation(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Operation-Location", server.URL+"/results/1")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_, _ = w.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()
	t.Setenv("AZURE_OCR_ENDPOINT", server.URL)
	t.Setenv("AZURE_OCR_API_KEY", "key")
	_, _, err := New().ExtractText(t.Context(), providers.Config{Timeout: 20 * time.Millisecond}, "page.png", base64.StdEncoding.EncodeToString([]byte("image")))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}
