package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "ollama" {
		t.Errorf("Expected name 'ollama', got '%s'", p.Name())
	}
}

func TestProvider_ValidateConfig(t *testing.T) {
	p := New()

	// Ollama validation is very simple - it just returns nil
	config := providers.Config{
		Provider: "ollama",
		Model:    "llava",
		Prompt:   "Extract text",
	}

	err := p.ValidateConfig(config)
	if err != nil {
		t.Errorf("Expected no error for Ollama validation, got: %v", err)
	}
}

func TestProvider_ExtractText(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		statusCode     int
		expectedText   string
		expectError    bool
		errorContains  string
	}{
		{
			name:       "successful response",
			statusCode: http.StatusOK,
			serverResponse: `{
				"model": "llava",
				"created_at": "2023-08-04T08:52:19.385406455Z",
				"response": "This is text extracted by Ollama",
				"done": true
			}`,
			expectedText: "This is text extracted by Ollama",
			expectError:  false,
		},
		{
			name:       "response with cleaning needed",
			statusCode: http.StatusOK,
			serverResponse: `{
				"model": "llava",
				"response": "I can see text that says: Important document content",
				"done": true
			}`,
			expectedText: "Important document content",
			expectError:  false,
		},
		{
			name:       "API error response",
			statusCode: http.StatusInternalServerError,
			serverResponse: `{
				"error": "Model not found"
			}`,
			expectError:   true,
			errorContains: "ollama API error",
		},
		{
			name:       "missing response field",
			statusCode: http.StatusOK,
			serverResponse: `{
				"model": "llava",
				"done": true
			}`,
			expectError:   true,
			errorContains: "no response from Ollama",
		},
		{
			name:       "malformed JSON",
			statusCode: http.StatusOK,
			serverResponse: `{
				"invalid": json
			}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method and headers
				if r.Method != http.MethodPost {
					t.Errorf("Expected POST request, got %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected application/json content type")
				}

				// Verify request path
				if !strings.Contains(r.URL.Path, "/api/generate") {
					t.Errorf("Expected /api/generate path, got %s", r.URL.Path)
				}

				// Verify request body structure
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				} else {
					if model, ok := reqBody["model"].(string); !ok || model == "" {
						t.Error("Expected model in request body")
					}
					if prompt, ok := reqBody["prompt"].(string); !ok || prompt == "" {
						t.Error("Expected prompt in request body")
					}
					if images, ok := reqBody["images"].([]interface{}); !ok || len(images) == 0 {
						t.Error("Expected images array in request body")
					}
					if stream, ok := reqBody["stream"].(bool); !ok || stream != false {
						t.Error("Expected stream to be false in request body")
					}
					if options, ok := reqBody["options"].(map[string]interface{}); !ok {
						t.Error("Expected options in request body")
					} else if temp, ok := options["temperature"]; !ok {
						t.Error("Expected temperature in options")
					} else if _, ok := temp.(float64); !ok {
						t.Error("Expected temperature to be a number")
					}
				}

				w.WriteHeader(tt.statusCode)
				if _, err := w.Write([]byte(tt.serverResponse)); err != nil {
					t.Errorf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Set environment variable to use test server
			original := os.Getenv("OLLAMA_URL")
			defer os.Setenv("OLLAMA_URL", original)
			os.Setenv("OLLAMA_URL", server.URL)

			p := New()
			config := providers.Config{
				Provider:    "ollama",
				Model:       "llava",
				Prompt:      "Extract all text from this image",
				Temperature: 0.3,
			}

			result, err := p.ExtractText(context.Background(), config, "test.jpg", "dGVzdCBpbWFnZSBkYXRh") // "test image data" in base64

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
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
		})
	}
}

func TestProvider_ExtractText_DefaultURL(t *testing.T) {
	// Test that default URL is used when OLLAMA_URL is not set
	original := os.Getenv("OLLAMA_URL")
	defer os.Setenv("OLLAMA_URL", original)
	os.Setenv("OLLAMA_URL", "")

	// Create a mock server that we won't actually hit
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Should not hit this server when testing default URL behavior")
	}))
	defer server.Close()

	p := New()
	config := providers.Config{
		Provider:    "ollama",
		Model:       "llava",
		Prompt:      "Extract all text from this image",
		Temperature: 0.0,
	}

	// This will fail to connect, but we're testing that it tries the default URL
	_, err := p.ExtractText(context.Background(), config, "test.jpg", "dGVzdA==")

	// We expect an error (connection refused), but we want to make sure it's trying the right URL
	if err == nil {
		t.Error("Expected connection error when trying to connect to default Ollama URL")
	}
	// The error should be a connection error, not an API parsing error
	if strings.Contains(err.Error(), "no response from Ollama") {
		t.Error("Got parsing error instead of connection error - indicates wrong URL format")
	}
}

func TestProvider_ExtractText_ModelDefaulting(t *testing.T) {
	// Test that model defaults to "llava" when gpt-4o or empty is provided
	tests := []struct {
		name          string
		inputModel    string
		expectedModel string
	}{
		{
			name:          "gpt-4o should default to llava",
			inputModel:    "gpt-4o",
			expectedModel: "llava",
		},
		{
			name:          "empty should default to llava",
			inputModel:    "",
			expectedModel: "llava",
		},
		{
			name:          "llama should stay llama",
			inputModel:    "llama",
			expectedModel: "llama",
		},
		{
			name:          "custom model should stay custom",
			inputModel:    "custom-vision-model",
			expectedModel: "custom-vision-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestModel string

			// Create mock server to capture the request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
					return
				}
				if model, ok := reqBody["model"].(string); ok {
					requestModel = model
				}

				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte(`{"response": "test response", "done": true}`)); err != nil {
					t.Errorf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Set environment variable to use test server
			original := os.Getenv("OLLAMA_URL")
			defer os.Setenv("OLLAMA_URL", original)
			os.Setenv("OLLAMA_URL", server.URL)

			p := New()
			config := providers.Config{
				Provider: "ollama",
				Model:    tt.inputModel,
				Prompt:   "Extract text",
			}

			_, err := p.ExtractText(context.Background(), config, "test.jpg", "dGVzdA==")
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if requestModel != tt.expectedModel {
				t.Errorf("Expected model '%s' in request, got '%s'", tt.expectedModel, requestModel)
			}
		})
	}
}

func TestCleanResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no cleaning needed",
			input:    "Simple text content",
			expected: "Simple text content",
		},
		{
			name:     "remove text reading prefix",
			input:    "I can see text that says: Important content",
			expected: "Important content",
		},
		{
			name:     "remove image text prefix",
			input:    "The text in the image reads: Document text",
			expected: "Document text",
		},
		{
			name:     "remove image contains prefix",
			input:    "The image contains text: Extracted content",
			expected: "Extracted content",
		},
		{
			name:     "remove here's text prefix",
			input:    "Here's the text from the image: Final content",
			expected: "Final content",
		},
		{
			name:     "remove quotes",
			input:    `"Quoted text content"`,
			expected: "Quoted text content",
		},
		{
			name:     "remove code blocks",
			input:    "```\nCode block content\n```",
			expected: "Code block content",
		},
		{
			name:     "trim whitespace",
			input:    "   Spaced content   ",
			expected: "Spaced content",
		},
		{
			name:     "complex cleaning",
			input:    "   \"```I can see text reading: Real content```\"   ",
			expected: "I can see text reading: Real content",
		},
		{
			name:     "case insensitive prefix removal",
			input:    "THE TEXT IN THE IMAGE IS: Upper case content",
			expected: "Upper case content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New()
			result := providers.ProcessResponse(p, tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
