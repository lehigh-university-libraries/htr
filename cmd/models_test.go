package cmd

import (
	"github.com/spf13/cobra"
	"testing"
)

func TestProviderModelDefaults(t *testing.T) {
	cases := []struct{ provider, environment, model string }{
		{"openai", "OPENAI_MODEL", "gpt-6.1-sol"},
		{"azure", "AZURE_MODEL", "prebuilt-read"},
		{"claude", "CLAUDE_MODEL", "claude-sonnet-5-5"},
		{"gemini", "GEMINI_MODEL", "gemini-4-argon"},
		{"ollama", "OLLAMA_MODEL", "gemma4:e4b"},
	}
	for _, tt := range cases {
		t.Run(tt.provider, func(t *testing.T) {
			t.Setenv(tt.environment, "")
			if got := getDefaultModel(tt.provider); got != tt.model {
				t.Fatalf("default=%q want=%q", got, tt.model)
			}
			t.Setenv(tt.environment, "pinned-model")
			if got := getDefaultModel(tt.provider); got != "pinned-model" {
				t.Fatalf("override=%q", got)
			}
		})
	}
	for _, command := range []*cobra.Command{evalCmd, ocrCmd, layoutCmd, createCmd} {
		if flag := command.Flags().Lookup("model"); flag.DefValue != "" {
			t.Errorf("%s forces model %s across providers", command.Name(), flag.DefValue)
		}
	}
}
