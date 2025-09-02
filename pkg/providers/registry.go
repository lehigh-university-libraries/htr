package providers

import (
	"fmt"
	"strings"
)

// Registry manages all available providers
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry
func (r *Registry) Register(provider Provider) {
	r.providers[strings.ToLower(provider.Name())] = provider
}

// Get retrieves a provider by name
func (r *Registry) Get(name string) (Provider, error) {
	provider, exists := r.providers[strings.ToLower(name)]
	if !exists {
		return nil, fmt.Errorf("provider %s not found", name)
	}
	return provider, nil
}

// List returns all available provider names
func (r *Registry) List() []string {
	var names []string
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// HasProvider checks if a provider is registered
func (r *Registry) HasProvider(name string) bool {
	_, exists := r.providers[strings.ToLower(name)]
	return exists
}
