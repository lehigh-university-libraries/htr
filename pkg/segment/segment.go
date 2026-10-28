// Package segment defines geometry-aware OCR provider contracts.
package segment

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	"github.com/lehigh-university-libraries/htr/pkg/providers"
)

// Capabilities declares explicit geometry and optional output features.
type Capabilities struct {
	Levels       []ocrpage.Level `json:"levels"`
	Polygons     bool            `json:"polygons"`
	Baselines    bool            `json:"baselines"`
	ReadingOrder bool            `json:"readingOrder"`
	RegionTypes  bool            `json:"regionTypes"`
	Text         bool            `json:"text"`
	Confidence   bool            `json:"confidence"`
	Tables       bool            `json:"tables"`
	Fields       bool            `json:"fields"`
}

// Hints carries document policy and optional language hints.
type Hints struct {
	DocumentType string   `json:"documentType,omitempty"`
	Languages    []string `json:"languages,omitempty"`
}

// Request contains encoded image bytes, model and document hints.
type Request struct {
	Image providers.Image
	Model string
	Hints Hints
}

// Result preserves canonical geometry and the native response for re-parsing.
type Result struct {
	Page           ocrpage.Page
	EffectiveModel string
	Usage          providers.UsageInfo
	Raw            []byte
	RawFormat      string
}

// Segmenter produces ordered page geometry.
type Segmenter interface {
	Name() string
	Capabilities(string) Capabilities
	Segment(context.Context, Request) (Result, error)
}

// FileSegmenter avoids copying an already prepared local image.
type FileSegmenter interface {
	SegmentFile(context.Context, string, Request) (Result, error)
}

// RequireLevel returns an actionable error when a provider lacks geometry.
func RequireLevel(s Segmenter, model string, level ocrpage.Level) error {
	if err := ocrpage.CheckLevel(level); err != nil {
		return err
	}
	c := s.Capabilities(model)
	if !slices.Contains(c.Levels, level) {
		return fmt.Errorf("segment: provider %q has no %s geometry (available: %v)", s.Name(), level, c.Levels)
	}
	return nil
}

// Registry stores segmenters; configure it before concurrent use.
type Registry struct{ providers map[string]Segmenter }

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry { return &Registry{providers: map[string]Segmenter{}} }

// Register registers or replaces a named provider.
func (r *Registry) Register(s Segmenter) { r.providers[strings.ToLower(s.Name())] = s }

// Get looks up a registered provider.
func (r *Registry) Get(name string) (Segmenter, error) {
	s, ok := r.providers[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("segment: unknown provider %q", name)
	}
	return s, nil
}

// List returns sorted provider names.
func (r *Registry) List() []string {
	v := make([]string, 0, len(r.providers))
	for k := range r.providers {
		v = append(v, k)
	}
	slices.Sort(v)
	return v
}
