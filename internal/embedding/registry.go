package embedding

import (
	"fmt"

	"github.com/vndee/memex/internal/cloudauth"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/httpclient"
)

// Registry creates embedding providers from KB config.
type Registry struct{}

// NewRegistry returns a new embedding provider registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// NewProvider creates an embedding provider from the given config.
func (r *Registry) NewProvider(cfg domain.EmbedConfig) (Provider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("embedding: model name is required")
	}
	switch cfg.Provider {
	case domain.ProviderOllama:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		if err := httpclient.ValidateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("embedding: %w", err)
		}
		return NewOllamaProvider(baseURL, cfg.Model, cfg.Dim), nil

	case domain.ProviderOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("embedding: openai provider requires an API key")
		}
		if cfg.BaseURL != "" {
			if err := httpclient.ValidateBaseURL(cfg.BaseURL); err != nil {
				return nil, fmt.Errorf("embedding: %w", err)
			}
		}
		return NewOpenAIProvider(cfg.BaseURL, cfg.Model, cfg.APIKey, cfg.Dim), nil

	case domain.ProviderGenAI, domain.ProviderGemini:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = cloudauth.GenAIBaseURL
		}
		if err := httpclient.ValidateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("embedding: %w", err)
		}
		apiKey := cloudauth.ResolveGoogleAPIKey(cfg.APIKey)
		return NewOpenAIProvider(baseURL, cfg.Model, apiKey, cfg.Dim), nil

	case domain.ProviderVertex:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			var err error
			baseURL, err = cloudauth.VertexBaseURL()
			if err != nil {
				return nil, err
			}
		}
		if err := httpclient.ValidateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("embedding: %w", err)
		}
		client, err := cloudauth.VertexClient()
		if err != nil {
			return nil, err
		}
		return NewOpenAIProviderWithClient(baseURL, cfg.Model, "", cfg.Dim, client), nil

	case domain.ProviderAzure, domain.ProviderGroq:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("embedding: %s provider requires an API key", cfg.Provider)
		}
		if cfg.BaseURL != "" {
			if err := httpclient.ValidateBaseURL(cfg.BaseURL); err != nil {
				return nil, fmt.Errorf("embedding: %w", err)
			}
		}
		return NewOpenAIProvider(cfg.BaseURL, cfg.Model, cfg.APIKey, cfg.Dim), nil

	default:
		return nil, fmt.Errorf("unknown embedding provider: %q", cfg.Provider)
	}
}
