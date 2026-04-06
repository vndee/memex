package embedding

import (
	"context"
	"fmt"
	"sync"

	"github.com/vndee/memex/internal/httpclient"
)

// OpenAIProvider calls an OpenAI-compatible /embeddings endpoint.
// Works with OpenAI, GenAI (Gemini), Vertex AI, Azure OpenAI, Groq, etc.
//
// The baseURL should include the API version prefix:
//   - OpenAI:    "https://api.openai.com/v1"
//   - GenAI:     "https://generativelanguage.googleapis.com/v1beta/openai"
//   - Vertex AI: "https://{LOCATION}-aiplatform.googleapis.com/v1beta1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi"
type OpenAIProvider struct {
	baseURL string
	model   string
	apiKey  string
	mu      sync.RWMutex
	dim     int
	client  *httpclient.Client
}

// NewOpenAIProvider creates an OpenAI-compatible embedding provider.
func NewOpenAIProvider(baseURL, model, apiKey string, dim int) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		dim:     dim,
		client:  httpclient.New(),
	}
}

// NewOpenAIProviderWithClient creates a provider with a custom HTTP client
// (e.g., one with OAuth2 transport for Vertex AI ADC).
func NewOpenAIProviderWithClient(baseURL, model, apiKey string, dim int, client *httpclient.Client) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		dim:     dim,
		client:  client,
	}
}

type openaiEmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed returns the embedding vector for a single text.
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.doEmbed(ctx, text)
	if err != nil {
		return nil, err
	}
	results := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		results[i] = d.Embedding
	}
	p.mu.RLock()
	currentDim := p.dim
	p.mu.RUnlock()
	dim, err := validateBatchResponse("openai", 1, currentDim, results)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.dim = dim
	p.mu.Unlock()
	result := results[0]
	return result, nil
}

// EmbedBatch returns embeddings for multiple texts.
func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := p.doEmbed(ctx, texts)
	if err != nil {
		return nil, err
	}
	results := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		results[i] = d.Embedding
	}
	p.mu.RLock()
	currentDim := p.dim
	p.mu.RUnlock()
	dim, err := validateBatchResponse("openai", len(texts), currentDim, results)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.dim = dim
	p.mu.Unlock()
	return results, nil
}

// Dimensions returns the embedding dimension size.
func (p *OpenAIProvider) Dimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dim
}

func (p *OpenAIProvider) doEmbed(ctx context.Context, input any) (*openaiEmbedResponse, error) {
	var resp openaiEmbedResponse
	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	err := p.client.DoJSON(ctx, p.baseURL+"/embeddings", headers, openaiEmbedRequest{
		Model: p.model,
		Input: input,
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return &resp, nil
}
