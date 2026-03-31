package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

type stubOCRProvider struct {
	validateConfig providers.Config
	extractConfig  providers.Config
	imagePath      string
	imageBase64    string
	response       string
}

func (p *stubOCRProvider) Name() string {
	return "openai"
}

func (p *stubOCRProvider) ValidateConfig(config providers.Config) error {
	p.validateConfig = config
	return nil
}

func (p *stubOCRProvider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
	p.extractConfig = config
	p.imagePath = imagePath
	p.imageBase64 = imageBase64
	return p.response, providers.UsageInfo{}, nil
}

func TestBuildOCRConfigUsesDefaults(t *testing.T) {
	originalImagePath := ocrImagePath
	originalProvider := ocrProvider
	originalModel := ocrModel
	originalPrompt := ocrPrompt
	originalTemperature := ocrTemperature
	originalTimeout := ocrTimeout
	originalMaxResolution := ocrMaxResolution
	originalMaxResolutionFallback := ocrMaxResolutionFallback
	t.Cleanup(func() {
		ocrImagePath = originalImagePath
		ocrProvider = originalProvider
		ocrModel = originalModel
		ocrPrompt = originalPrompt
		ocrTemperature = originalTemperature
		ocrTimeout = originalTimeout
		ocrMaxResolution = originalMaxResolution
		ocrMaxResolutionFallback = originalMaxResolutionFallback
	})

	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "page.jpg")
	if err := os.WriteFile(imagePath, []byte("image-data"), 0644); err != nil {
		t.Fatalf("failed to create temp image: %v", err)
	}

	t.Setenv("OPENAI_MODEL", "gpt-test")

	ocrImagePath = imagePath
	ocrProvider = "openai"
	ocrModel = ""
	ocrPrompt = defaultOCRPrompt
	ocrTemperature = 0.3
	ocrTimeout = 42 * time.Second
	ocrMaxResolution = "MEDIA_RESOLUTION_HIGH"
	ocrMaxResolutionFallback = true

	config, err := buildOCRConfig()
	if err != nil {
		t.Fatalf("buildOCRConfig() error = %v", err)
	}

	if config.Model != "gpt-test" {
		t.Fatalf("config.Model = %q, want %q", config.Model, "gpt-test")
	}
	if config.Prompt != defaultOCRPrompt {
		t.Fatalf("config.Prompt = %q, want %q", config.Prompt, defaultOCRPrompt)
	}
	if config.MaxResolution != "MEDIA_RESOLUTION_HIGH" {
		t.Fatalf("config.MaxResolution = %q, want %q", config.MaxResolution, "MEDIA_RESOLUTION_HIGH")
	}
	if !config.MaxResolutionFallback {
		t.Fatal("config.MaxResolutionFallback = false, want true")
	}
}

func TestRunOCRWritesExtractedTextToFile(t *testing.T) {
	originalRegistry := providerRegistry
	originalImagePath := ocrImagePath
	originalProvider := ocrProvider
	originalModel := ocrModel
	originalPrompt := ocrPrompt
	originalTemperature := ocrTemperature
	originalTimeout := ocrTimeout
	originalOutputPath := ocrOutputPath
	originalMaxResolution := ocrMaxResolution
	originalMaxResolutionFallback := ocrMaxResolutionFallback
	t.Cleanup(func() {
		providerRegistry = originalRegistry
		ocrImagePath = originalImagePath
		ocrProvider = originalProvider
		ocrModel = originalModel
		ocrPrompt = originalPrompt
		ocrTemperature = originalTemperature
		ocrTimeout = originalTimeout
		ocrOutputPath = originalOutputPath
		ocrMaxResolution = originalMaxResolution
		ocrMaxResolutionFallback = originalMaxResolutionFallback
	})

	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "page.jpg")
	outputPath := filepath.Join(tmpDir, "page.txt")
	if err := os.WriteFile(imagePath, []byte("image-data"), 0644); err != nil {
		t.Fatalf("failed to create temp image: %v", err)
	}

	stub := &stubOCRProvider{response: "hello from OCR"}
	registry := providers.NewRegistry()
	registry.Register(stub)
	providerRegistry = registry

	ocrImagePath = imagePath
	ocrProvider = "openai"
	ocrModel = ""
	ocrPrompt = defaultOCRPrompt
	ocrTemperature = 0.0
	ocrTimeout = 5 * time.Second
	ocrOutputPath = outputPath
	ocrMaxResolution = "MEDIA_RESOLUTION_UNSPECIFIED"
	ocrMaxResolutionFallback = false

	t.Setenv("OPENAI_MODEL", "gpt-test")

	if err := runOCR(nil, nil); err != nil {
		t.Fatalf("runOCR() error = %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read OCR output: %v", err)
	}

	if string(data) != "hello from OCR" {
		t.Fatalf("OCR output = %q, want %q", string(data), "hello from OCR")
	}
	if stub.validateConfig.Model != "gpt-test" {
		t.Fatalf("validated model = %q, want %q", stub.validateConfig.Model, "gpt-test")
	}
	if stub.extractConfig.Prompt != defaultOCRPrompt {
		t.Fatalf("extract prompt = %q, want %q", stub.extractConfig.Prompt, defaultOCRPrompt)
	}
	if stub.imagePath != imagePath {
		t.Fatalf("imagePath = %q, want %q", stub.imagePath, imagePath)
	}
	if strings.TrimSpace(stub.imageBase64) == "" {
		t.Fatal("imageBase64 should not be empty")
	}
}
