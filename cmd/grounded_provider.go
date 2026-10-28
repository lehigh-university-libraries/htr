package cmd

import (
	"context"

	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// groundedProvider exposes a geometry provider's transcription client to eval.
type groundedProvider struct{ name string }

func (p groundedProvider) Name() string { return p.name }
func (p groundedProvider) ValidateConfig(c providers.Config) error {
	_, e := segmentationProvider(p.name, c.Model)
	return e
}
func (p groundedProvider) ExtractText(ctx context.Context, c providers.Config, path, encoded string) (string, providers.UsageInfo, error) {
	r, e := providers.LegacyRequest(c, path, encoded, 50<<20)
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	s, e := segmentationProvider(p.name, c.Model)
	if e != nil {
		return "", providers.UsageInfo{}, e
	}
	client, ok := s.(providers.Client)
	if !ok {
		return "", providers.UsageInfo{}, providers.NewError(providers.ErrorInvalidRequest, 0, false, nil)
	}
	result, e := client.Extract(ctx, r)
	return result.Text, result.Usage, e
}
