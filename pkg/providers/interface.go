package providers

import "context"

// Config represents the configuration for a provider
type Config struct {
	Provider    string
	Model       string
	Prompt      string
	Temperature float64
}

// Provider interface that all OCR/vision providers must implement
type Provider interface {
	// ExtractText extracts text from an image using the provider's API
	ExtractText(ctx context.Context, config Config, imagePath, imageBase64 string) (string, error)
	// Name returns the provider's name
	Name() string
	// ValidateConfig validates the provider-specific configuration
	ValidateConfig(config Config) error
}
