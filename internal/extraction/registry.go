package extraction

import (
	"fmt"

	"github.com/vndee/memex/internal/cloudauth"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/httpclient"
)

// Registry creates LLM extraction providers from KB config.
type Registry struct{}

// NewRegistry returns a new extraction provider registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// NewProvider creates an extraction provider from the given config.
func (r *Registry) NewProvider(cfg domain.LLMConfig) (Provider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("extraction: model name is required")
	}
	switch cfg.Provider {
	case domain.ProviderOllama:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		if err := httpclient.ValidateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("extraction: %w", err)
		}
		return NewOllamaProvider(baseURL, cfg.Model), nil

	case domain.ProviderOpenAI:
		if cfg.BaseURL != "" {
			if err := httpclient.ValidateBaseURL(cfg.BaseURL); err != nil {
				return nil, fmt.Errorf("extraction: %w", err)
			}
		}
		return NewOpenAIProvider(cfg.BaseURL, cfg.Model, cfg.APIKey), nil

	case domain.ProviderGenAI, domain.ProviderGemini:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = cloudauth.GenAIBaseURL
		}
		if err := httpclient.ValidateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("extraction: %w", err)
		}
		apiKey := cloudauth.ResolveGoogleAPIKey(cfg.APIKey)
		return NewOpenAIProvider(baseURL, cfg.Model, apiKey), nil

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
			return nil, fmt.Errorf("extraction: %w", err)
		}
		client, err := cloudauth.VertexClient()
		if err != nil {
			return nil, err
		}
		return NewOpenAIProviderWithClient(baseURL, cfg.Model, "", client), nil

	case domain.ProviderAzure, domain.ProviderGroq:
		if cfg.BaseURL != "" {
			if err := httpclient.ValidateBaseURL(cfg.BaseURL); err != nil {
				return nil, fmt.Errorf("extraction: %w", err)
			}
		}
		return NewOpenAIProvider(cfg.BaseURL, cfg.Model, cfg.APIKey), nil

	default:
		return nil, fmt.Errorf("unknown LLM provider: %q", cfg.Provider)
	}
}
