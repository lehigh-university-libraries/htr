package cmd

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultOCRPrompt = "Extract all text from this image. Return only the transcribed text."

var ocrCmd = &cobra.Command{
	Use:   "ocr",
	Short: "Extract OCR text from a single image",
	Long: `Extract OCR text from a single image using a configured provider and model.

This command sends one image to the selected OCR/vision provider and prints the
transcribed text directly, without running an evaluation.`,
	RunE: runOCR,
}

var (
	ocrImagePath             string
	ocrProvider              string
	ocrModel                 string
	ocrPrompt                string
	ocrTemperature           float64
	ocrTimeout               time.Duration
	ocrOutputPath            string
	ocrDebug                 bool
	ocrMaxResolution         string
	ocrMaxResolutionFallback bool
)

func init() {
	RootCmd.AddCommand(ocrCmd)

	ocrCmd.Flags().StringVar(&ocrImagePath, "image", "", "Path or URL to the input image")
	ocrCmd.Flags().StringVar(&ocrProvider, "provider", "openai", "Provider to use: openai, azure, claude, gemini, ollama")
	ocrCmd.Flags().StringVarP(&ocrModel, "model", "m", "", "Model to use (uses provider default when available)")
	ocrCmd.Flags().StringVarP(&ocrPrompt, "prompt", "p", defaultOCRPrompt, "Prompt to send to the provider")
	ocrCmd.Flags().Float64VarP(&ocrTemperature, "temperature", "t", 0.0, "Temperature for API")
	ocrCmd.Flags().DurationVar(&ocrTimeout, "timeout", 5*time.Minute, "Timeout for API requests (e.g., 5m, 30s, 1h)")
	ocrCmd.Flags().StringVarP(&ocrOutputPath, "output", "o", "", "Write OCR text to a file instead of stdout")
	ocrCmd.Flags().BoolVar(&ocrDebug, "debug", false, "Print provider debug output when supported")
	ocrCmd.Flags().StringVar(&ocrMaxResolution, "gemini-max-resolution", "MEDIA_RESOLUTION_UNSPECIFIED", "Max resolution for Gemini models (e.g., MEDIA_RESOLUTION_HIGH)")
	ocrCmd.Flags().BoolVar(&ocrMaxResolutionFallback, "gemini-max-resolution-fallback", false, "Automatically retry with lower resolution if MAX_TOKENS error occurs")

	err := ocrCmd.MarkFlagRequired("image")
	if err != nil {
		panic(err)
	}
}

func runOCR(cmd *cobra.Command, args []string) error {
	config, err := buildOCRConfig()
	if err != nil {
		return err
	}

	text, err := processOCRImage(config, ocrImagePath)
	if err != nil {
		return err
	}

	return outputOCRText(text, ocrOutputPath)
}

func buildOCRConfig() (EvalConfig, error) {
	if !slices.Contains(allowedMediaResolutions, ocrMaxResolution) {
		return EvalConfig{}, fmt.Errorf("invalid --gemini-max-resolution value '%s'. Allowed values are: %s", ocrMaxResolution, strings.Join(allowedMediaResolutions, ", "))
	}

	if !isRemoteResource(ocrImagePath) {
		if _, err := os.Stat(ocrImagePath); err != nil {
			return EvalConfig{}, fmt.Errorf("failed to access image %s: %w", ocrImagePath, err)
		}
	}

	model := ocrModel
	if model == "" {
		model = getDefaultModel(ocrProvider)
	}

	return EvalConfig{
		Provider:              ocrProvider,
		Model:                 model,
		Prompt:                ocrPrompt,
		Temperature:           ocrTemperature,
		Timeout:               ocrTimeout,
		Debug:                 ocrDebug,
		MaxResolution:         ocrMaxResolution,
		MaxResolutionFallback: ocrMaxResolutionFallback,
	}, nil
}

func processOCRImage(config EvalConfig, imagePath string) (string, error) {
	imageBase64, err := getImageAsBase64(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to process image: %w", err)
	}

	text, _, err := extractTextWithProvider(config, imagePath, imageBase64)
	if err != nil {
		return "", fmt.Errorf("provider API call failed: %w", err)
	}

	return text, nil
}

func outputOCRText(text, outputPath string) error {
	if outputPath != "" {
		return os.WriteFile(outputPath, []byte(text), 0644)
	}

	fmt.Println(text)
	return nil
}

func isRemoteResource(path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}

	return u.Scheme == "http" || u.Scheme == "https"
}
