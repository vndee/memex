package embedding

import (
	"context"
	"fmt"
	"sync"

	"github.com/vndee/memex/internal/httpclient"
)

// OllamaProvider calls Ollama's /api/embed endpoint.
type OllamaProvider struct {
	baseURL string
	model   string
	mu      sync.RWMutex
	dim     int
	client  *httpclient.Client
}

// NewOllamaProvider creates an Ollama embedding provider.
// If dim is 0, it will be detected on the first call.
func NewOllamaProvider(baseURL, model string, dim int) *OllamaProvider {
	return &OllamaProvider{
		baseURL: baseURL,
		model:   model,
		dim:     dim,
		client:  httpclient.New(),
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns the embedding vector for a single text.
func (p *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	var resp ollamaEmbedResponse
	if err := p.doEmbed(ctx, text, &resp); err != nil {
		return nil, err
	}
	p.mu.RLock()
	currentDim := p.dim
	p.mu.RUnlock()
	dim, err := validateBatchResponse("ollama", 1, currentDim, resp.Embeddings)
	if err != nil {
		return nil, err
	}
	result := resp.Embeddings[0]
	p.mu.Lock()
	p.dim = dim
	p.mu.Unlock()
	return result, nil
}

// EmbedBatch returns embeddings for multiple texts.
func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp ollamaEmbedResponse
	if err := p.doEmbed(ctx, texts, &resp); err != nil {
		return nil, err
	}
	p.mu.RLock()
	currentDim := p.dim
	p.mu.RUnlock()
	dim, err := validateBatchResponse("ollama", len(texts), currentDim, resp.Embeddings)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.dim = dim
	p.mu.Unlock()
	return resp.Embeddings, nil
}

// Dimensions returns the embedding dimension size.
func (p *OllamaProvider) Dimensions() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dim
}

func (p *OllamaProvider) doEmbed(ctx context.Context, input any, resp *ollamaEmbedResponse) error {
	err := p.client.DoJSON(ctx, p.baseURL+"/api/embed", nil, ollamaEmbedRequest{
		Model: p.model,
		Input: input,
	}, resp)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	return nil
}
