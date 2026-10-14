package claude

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestCurrentModelsExtractText(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	for _, model := range []string{DefaultModel, "claude-opus-5-5", "claude-haiku-5-5", "claude-fable-5-1", "claude-sonnet-4-5-20250929"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing authentication/version")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				_, hasTemperature := body["temperature"]
				if hasTemperature == modernModel(model) {
					t.Errorf("temperature present=%v for %s", hasTemperature, model)
				}
				limit := float64(4096)
				if modernModel(model) {
					limit = 16000
				}
				if body["max_tokens"] != limit || body["model"] != model {
					t.Errorf("body=%v", body)
				}
				_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"café "},{"type":"text","text":"世界"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":12}}`))
			}))
			defer server.Close()
			provider := &Provider{endpoint: server.URL}
			text, usage, err := provider.ExtractText(t.Context(), providers.Config{Model: model, Prompt: "Transcribe", Temperature: 0.5}, "page.png", base64.StdEncoding.EncodeToString([]byte("image")))
			if err != nil {
				t.Fatal(err)
			}
			if text != "café 世界" || usage.OutputTokens != 12 {
				t.Fatalf("text=%q usage=%+v", text, usage)
			}
		})
	}
}

func TestOutputLimitRetainsUsage(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"private"}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":16000}}`))
	}))
	defer server.Close()
	provider := &Provider{endpoint: server.URL}
	_, usage, err := provider.ExtractText(t.Context(), providers.Config{Model: DefaultModel, Prompt: "Transcribe"}, "page.png", base64.StdEncoding.EncodeToString([]byte("image")))
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) || providerErr.Kind != providers.ErrorOutputLimit || usage.OutputTokens != 16000 {
		t.Fatalf("usage=%+v error=%v", usage, err)
	}
}
