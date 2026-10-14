package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/spf13/cobra"
)

func TestRunLayoutFillsTemplate(t *testing.T) {
	originalRegistry := providerRegistry
	originals := []string{layoutImagePath, layoutTemplatePath, layoutProvider, layoutModel, layoutOutputPath, layoutResponsePath, layoutMaxResolution, layoutThinkingLevel}
	t.Cleanup(func() {
		providerRegistry = originalRegistry
		layoutImagePath, layoutTemplatePath, layoutProvider, layoutModel, layoutOutputPath, layoutResponsePath, layoutMaxResolution, layoutThinkingLevel =
			originals[0], originals[1], originals[2], originals[3], originals[4], originals[5], originals[6], originals[7]
	})

	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stub := &stubOCRProvider{response: `<PcGts><TextLine id="l1"><TextEquiv><Unicode>Hello</Unicode></TextEquiv></TextLine></PcGts>`}
	providerRegistry = providers.NewRegistry()
	providerRegistry.Register(stub)

	layoutImagePath = write("page.png", "\x89PNG\r\n\x1a\n")
	layoutTemplatePath = write("page.xml", `<PcGts><Page><TextRegion id="r1"><TextLine id="l1"><Coords points="0,0 1,1"/></TextLine></TextRegion></Page></PcGts>`)
	layoutProvider = "openai"
	layoutModel = "gpt-test"
	layoutOutputPath = filepath.Join(dir, "filled.xml")
	layoutResponsePath = filepath.Join(dir, "response.xml")
	layoutMaxResolution = "MEDIA_RESOLUTION_UNSPECIFIED"
	layoutThinkingLevel = "high"

	command := &cobra.Command{}
	command.SetContext(t.Context())
	if err := runLayout(command, nil); err != nil {
		t.Fatal(err)
	}
	filled, err := os.ReadFile(layoutOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `<TextLine id="l1"><Coords points="0,0 1,1"/><TextEquiv><Unicode>Hello</Unicode></TextEquiv></TextLine><TextEquiv><Unicode>Hello</Unicode></TextEquiv></TextRegion>`
	if !strings.Contains(string(filled), want) {
		t.Fatalf("filled = %s", filled)
	}
	if raw, err := os.ReadFile(layoutResponsePath); err != nil || string(raw) != stub.response {
		t.Fatalf("raw response = %q, err = %v", raw, err)
	}
	if stub.extractConfig.Model != "gpt-test" || stub.extractConfig.ThinkingLevel != "high" || !strings.Contains(stub.extractConfig.Prompt, `<TextLine id="l1">`) || stub.imagePath != "page.png" {
		t.Fatalf("unexpected provider call: %#v image=%s", stub.extractConfig, stub.imagePath)
	}
}

func TestValidateGeminiSpendFlags(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		maxTokens int
		wantErr   bool
	}{
		{name: "defaults", level: "", maxTokens: 0},
		{name: "upper case level", level: "HIGH", maxTokens: 32768},
		{name: "unknown level", level: "extreme", wantErr: true},
		{name: "negative budget", level: "low", maxTokens: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateGeminiSpendFlags(tt.level, tt.maxTokens); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLayoutTemplateOptions(t *testing.T) {
	oldTemplate, oldURL, oldToken, oldOutput, oldFormat := layoutTemplatePath, layoutSegmentorURL, layoutSegmentorTokenFile, layoutTemplateOutput, layoutFormat
	t.Cleanup(func() {
		layoutTemplatePath, layoutSegmentorURL, layoutSegmentorTokenFile, layoutTemplateOutput, layoutFormat = oldTemplate, oldURL, oldToken, oldOutput, oldFormat
	})
	tests := []struct{ name, template, url, token, output, format, want string }{
		{name: "missing source", want: "provide exactly one"},
		{name: "two sources", template: "page.xml", url: "https://example.run.app", want: "provide exactly one"},
		{name: "unused token", template: "page.xml", token: "token", want: "require --segmentor-url"},
		{name: "unused output", template: "page.xml", output: "blank.xml", want: "require --segmentor-url"},
		{name: "wrong format", url: "https://example.run.app", format: "hocr", want: "produces PageXML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layoutTemplatePath, layoutSegmentorURL, layoutSegmentorTokenFile, layoutTemplateOutput, layoutFormat = tt.template, tt.url, tt.token, tt.output, tt.format
			err := runLayout(&cobra.Command{}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v; want %s", err, tt.want)
			}
		})
	}
}
