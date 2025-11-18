package utils

import (
	"errors"
	"testing"
)

func TestMaskSensitiveData(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "no sensitive data",
			input:    "this is a normal error message",
			expected: "this is a normal error message",
		},
		{
			name:     "gemini URL with API key",
			input:    "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ",
			expected: "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=***MASKED***",
		},
		{
			name:     "URL with api_key parameter",
			input:    "https://example.com/api?api_key=secret123&other=value",
			expected: "https://example.com/api?api_key=***MASKED***&other=value",
		},
		{
			name:     "URL with apiKey parameter",
			input:    "https://example.com/api?apiKey=secret123",
			expected: "https://example.com/api?apiKey=***MASKED***",
		},
		{
			name:     "Bearer token in Authorization header",
			input:    "Authorization: Bearer sk-proj-ABC123DEF456",
			expected: "Authorization: Bearer ***MASKED***",
		},
		{
			name:     "Azure Ocp-Apim-Subscription-Key header",
			input:    "Ocp-Apim-Subscription-Key: 1234567890abcdef",
			expected: "Ocp-Apim-Subscription-Key: ***MASKED***",
		},
		{
			name:     "Anthropic x-api-key header",
			input:    "x-api-key: sk-ant-abc123",
			expected: "x-api-key: ***MASKED***",
		},
		{
			name:     "multiple keys in same string",
			input:    "Error: failed to call https://api.example.com?key=secret123&other=value with Bearer token123",
			expected: "Error: failed to call https://api.example.com?key=***MASKED***&other=value with Bearer ***MASKED***",
		},
		{
			name:     "key in middle of URL parameters",
			input:    "https://example.com?param1=value1&key=secretkey&param2=value2",
			expected: "https://example.com?param1=value1&key=***MASKED***&param2=value2",
		},
		{
			name:     "error message with gemini URL",
			input:    "Post \"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=AIzaSyTest123\": context deadline exceeded",
			expected: "Post \"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=***MASKED***\": context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskSensitiveData(tt.input)
			if result != tt.expected {
				t.Errorf("MaskSensitiveData() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMaskSensitiveError(t *testing.T) {
	tests := []struct {
		name        string
		inputError  error
		shouldMask  bool
		expectedMsg string
	}{
		{
			name:        "nil error",
			inputError:  nil,
			shouldMask:  false,
			expectedMsg: "",
		},
		{
			name:        "error with API key in URL",
			inputError:  errors.New("failed to call https://api.example.com?key=secret123"),
			shouldMask:  true,
			expectedMsg: "failed to call https://api.example.com?key=***MASKED***",
		},
		{
			name:        "error with Bearer token",
			inputError:  errors.New("unauthorized: Bearer sk-test-token"),
			shouldMask:  true,
			expectedMsg: "unauthorized: Bearer ***MASKED***",
		},
		{
			name:        "normal error without sensitive data",
			inputError:  errors.New("connection refused"),
			shouldMask:  true,
			expectedMsg: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskSensitiveError(tt.inputError)

			if !tt.shouldMask {
				if result != nil {
					t.Errorf("MaskSensitiveError() should return nil for nil input, got %v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("MaskSensitiveError() should not return nil for non-nil input")
				return
			}

			if result.Error() != tt.expectedMsg {
				t.Errorf("MaskSensitiveError().Error() = %q, want %q", result.Error(), tt.expectedMsg)
			}
		})
	}
}

func TestMaskedErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original error with https://api.example.com?key=secret123")
	masked := MaskSensitiveError(originalErr)

	if masked == nil {
		t.Fatal("MaskSensitiveError should not return nil")
	}

	// Test that the error message is masked
	if masked.Error() == originalErr.Error() {
		t.Errorf("Masked error should have different message than original")
	}

	// Verify the masked message is correct
	expectedMasked := "original error with https://api.example.com?key=***MASKED***"
	if masked.Error() != expectedMasked {
		t.Errorf("Masked error message = %q, want %q", masked.Error(), expectedMasked)
	}

	// Test that we can unwrap to get the original error
	unwrapped := errors.Unwrap(masked)
	if unwrapped != originalErr {
		t.Errorf("Unwrap() should return the original error")
	}
}
