package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/lehigh-university-libraries/htr/pkg/azure"
	"github.com/lehigh-university-libraries/htr/pkg/claude"
	"github.com/lehigh-university-libraries/htr/pkg/gemini"
	"github.com/lehigh-university-libraries/htr/pkg/hocr"
	"github.com/lehigh-university-libraries/htr/pkg/ollama"
	"github.com/lehigh-university-libraries/htr/pkg/openai"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
	"github.com/spf13/cobra"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create hOCR XML file from an image",
	Long: `Create an hOCR XML file from an image using custom word detection and LLM transcription.

This command processes an image to detect word boundaries using image processing techniques,
creates a stitched image with hOCR markup overlays, and then uses a Language Model to
transcribe the text content, producing a complete hOCR XML output.`,
	RunE: runCreate,
}

var (
	imagePath   string
	provider    string
	model       string
	outputPath  string
	temperature float64
)

func init() {
	RootCmd.AddCommand(createCmd)

	createCmd.Flags().StringVar(&imagePath, "image", "", "Path to input image file (required)")
	createCmd.Flags().StringVar(&provider, "provider", "ollama", "Provider to use: openai, azure, claude, gemini, ollama")
	createCmd.Flags().StringVar(&model, "model", "", "Model to use (uses provider default if not specified)")
	createCmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output path for hOCR XML file (prints to stdout if not specified)")
	createCmd.Flags().Float64VarP(&temperature, "temperature", "t", 0.0, "Temperature for LLM")

	err := createCmd.MarkFlagRequired("image")
	if err != nil {
		slog.Error("Unable to mark image as required", "err", err)
		os.Exit(1)
	}
}

func runCreate(cmd *cobra.Command, args []string) error {
	// Validate input file exists
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return fmt.Errorf("input image file does not exist: %s", imagePath)
	}

	// Initialize provider registry
	registry := providers.NewRegistry()
	registry.Register(openai.New())
	registry.Register(azure.New())
	registry.Register(claude.New())
	registry.Register(gemini.New())
	registry.Register(ollama.New())

	// Get provider
	providerInstance, err := registry.Get(provider)
	if err != nil {
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	// Set default model if not specified
	if model == "" {
		model = getDefaultModel(provider)
	}

	slog.Info("Creating hOCR XML from image", "image", imagePath, "provider", provider, "model", model)

	// Step 1: Detect word boundaries using custom image processing
	ocrResponse, err := hocr.DetectWordBoundariesCustom(imagePath)
	if err != nil {
		return fmt.Errorf("failed to detect word boundaries: %w", err)
	}

	// Step 2: Configure provider for word transcription
	config := providers.Config{
		Provider:    provider,
		Model:       model,
		Temperature: temperature,
	}

	// Validate configuration
	if err := providerInstance.ValidateConfig(config); err != nil {
		return fmt.Errorf("provider configuration validation failed: %w", err)
	}

	// Step 3: Transcribe individual word images
	hocrContent, err := hocr.TranscribeWordsIndividually(imagePath, ocrResponse, providerInstance, config)
	if err != nil {
		slog.Warn("Individual word transcription failed, using basic hOCR", "error", err)
		basicHOCR := hocr.ConvertToBasicHOCR(ocrResponse)
		return outputResult(basicHOCR)
	}

	slog.Info("Individual word transcription completed", "content_length", len(hocrContent))

	// Step 4: Wrap in hOCR document and output
	finalHOCR := hocr.WrapInHOCRDocument(hocrContent)
	return outputResult(finalHOCR)
}

func outputResult(hocrXML string) error {
	if outputPath != "" {
		return os.WriteFile(outputPath, []byte(hocrXML), 0644)
	} else {
		fmt.Print(hocrXML)
		return nil
	}
}

func getDefaultModel(providerName string) string {
	switch providerName {
	case "openai":
		if model := os.Getenv("OPENAI_MODEL"); model != "" {
			return model
		}
		return "gpt-4o"
	case "azure":
		if model := os.Getenv("AZURE_MODEL"); model != "" {
			return model
		}
		return "gpt-4o"
	case "claude":
		if model := os.Getenv("CLAUDE_MODEL"); model != "" {
			return model
		}
		return "claude-sonnet-4-5-20250514"
	case "gemini":
		if model := os.Getenv("GEMINI_MODEL"); model != "" {
			return model
		}
		return "gemini-1.5-flash"
	case "ollama":
		if model := os.Getenv("OLLAMA_MODEL"); model != "" {
			return model
		}
		return "mistral-small3.2:24b"
	default:
		return ""
	}
}
