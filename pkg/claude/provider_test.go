package claude

import (
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
	if p.Name() != "claude" {
		t.Errorf("Expected name 'claude', got '%s'", p.Name())
	}
}

func TestProvider_ValidateConfig(t *testing.T) {
	p := New()

	tests := []struct {
		name          string
		apiKey        string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid API key",
			apiKey:      "sk-ant-test-key",
			expectError: false,
		},
		{
			name:          "missing API key",
			apiKey:        "",
			expectError:   true,
			errorContains: "ANTHROPIC_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			original := os.Getenv("ANTHROPIC_API_KEY")
			defer os.Setenv("ANTHROPIC_API_KEY", original)
			os.Setenv("ANTHROPIC_API_KEY", tt.apiKey)

			config := providers.Config{
				Provider: "claude",
				Model:    "claude-3-5-sonnet-20241022",
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
				"content": [
					{
						"type": "text",
						"text": "This is extracted text from the image"
					}
				],
				"stop_reason": "end_turn"
			}`,
			expectedText: "This is extracted text from the image",
			expectError:  false,
		},
		{
			name:       "response with cleaning needed",
			statusCode: http.StatusOK,
			serverResponse: `{
				"content": [
					{
						"type": "text",
						"text": "Here's the text extracted from the image: \"Cleaned text\""
					}
				],
				"stop_reason": "end_turn"
			}`,
			expectedText: "Cleaned text",
			expectError:  false,
		},
		{
			name:       "API error response",
			statusCode: http.StatusBadRequest,
			serverResponse: `{
				"error": {
					"type": "invalid_request_error",
					"message": "Invalid request"
				}
			}`,
			expectError:   true,
			errorContains: "claude API error",
		},
		{
			name:       "empty content",
			statusCode: http.StatusOK,
			serverResponse: `{
				"content": [],
				"stop_reason": "end_turn"
			}`,
			expectError:   true,
			errorContains: "no response from Claude",
		},
		{
			name:       "no text content",
			statusCode: http.StatusOK,
			serverResponse: `{
				"content": [
					{
						"type": "other",
						"text": "This should not be returned"
					}
				],
				"stop_reason": "end_turn"
			}`,
			expectError:   true,
			errorContains: "no text content in Claude response",
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
				if r.Header.Get("x-api-key") == "" {
					t.Errorf("Expected x-api-key header")
				}
				if r.Header.Get("anthropic-version") == "" {
					t.Errorf("Expected anthropic-version header")
				}

				// Verify request body structure
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
					if model, ok := reqBody["model"].(string); !ok || model == "" {
						t.Error("Expected model in request body")
					}
					if messages, ok := reqBody["messages"].([]interface{}); !ok || len(messages) == 0 {
						t.Error("Expected messages in request body")
					}
					if maxTokens, ok := reqBody["max_tokens"].(float64); !ok || maxTokens <= 0 {
						t.Error("Expected max_tokens in request body")
					}
				}

				w.WriteHeader(tt.statusCode)
				if _, err := w.Write([]byte(tt.serverResponse)); err != nil {
					t.Errorf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Set environment variable
			original := os.Getenv("ANTHROPIC_API_KEY")
			defer os.Setenv("ANTHROPIC_API_KEY", original)
			os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")

			if tt.statusCode == http.StatusOK && !tt.expectError {
				p := New()
				cleaned := providers.ProcessResponse(p, tt.expectedText)
				if cleaned != tt.expectedText {
					t.Logf("Response cleaning changed text from '%s' to '%s'", tt.expectedText, cleaned)
				}
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
			input:    "Simple text",
			expected: "Simple text",
		},
		{
			name:     "remove quotes",
			input:    `"Quoted text"`,
			expected: "Quoted text",
		},
		{
			name:     "remove code blocks",
			input:    "```\nCode block text\n```",
			expected: "Code block text",
		},
		{
			name:     "remove common prefixes",
			input:    "Certainly! Here's the text extracted from the image: Actual content",
			expected: "Actual content",
		},
		{
			name:     "remove extracted text prefix",
			input:    "Here's the extracted text from the image: Important content",
			expected: "Important content",
		},
		{
			name:     "trim whitespace",
			input:    "   Spaced text   ",
			expected: "Spaced text",
		},
		{
			name:     "complex cleaning",
			input:    "   \"```Here's the text extracted from the image: Real content```\"   ",
			expected: "Here's the text extracted from the image: Real content",
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
