package openai

import (
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"testing"
)

func TestCompatibleEndpointRequiresTLSExceptLoopback(t *testing.T) {
	t.Setenv("OPENAI_COMPATIBLE_API_KEY", "test")
	for _, tt := range []struct {
		endpoint string
		valid    bool
	}{
		{"https://example.org/chat/completions", true},
		{"http://example.org/chat/completions", false},
		{"http://localhost:8000/v1/chat/completions", true},
		{"http://127.0.0.1:8000/v1/chat/completions", true},
		{"http://[::1]:8000/v1/chat/completions", true},
		{"http://localhost.example.org/chat/completions", false},
	} {
		err := NewCompatible().ValidateConfig(providers.Config{Model: "test", BaseURL: tt.endpoint})
		if (err == nil) != tt.valid {
			t.Errorf("%s: %v", tt.endpoint, err)
		}
	}
}
