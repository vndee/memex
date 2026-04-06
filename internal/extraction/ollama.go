package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vndee/memex/internal/httpclient"
)

// OllamaProvider uses Ollama's /api/generate endpoint for extraction.
type OllamaProvider struct {
	baseURL string
	model   string
	client  *httpclient.Client
}

// NewOllamaProvider creates an Ollama LLM extraction provider.
func NewOllamaProvider(baseURL, model string) *OllamaProvider {
	return &OllamaProvider{
		baseURL: baseURL,
		model:   model,
		client:  httpclient.New(),
	}
}

type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format,omitempty"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
}

// Extract entities and relations from the given text.
func (p *OllamaProvider) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	resp, err := p.generate(ctx, buildExtractionPrompt(text), "json")
	if err != nil {
		return nil, fmt.Errorf("ollama extract: %w", err)
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("ollama extract: parse response: %w (raw: %s)", err, truncate(resp, 200))
	}

	normalizeResult(&result)
	return &result, nil
}

// Summarize produces a concise summary of the given text.
func (p *OllamaProvider) Summarize(ctx context.Context, text string) (string, error) {
	return p.generate(ctx, buildSummarizePrompt(text), "")
}

// ResolveEntity determines if two entity descriptions refer to the same entity.
func (p *OllamaProvider) ResolveEntity(ctx context.Context, a, b ExtractedEntity) (bool, error) {
	resp, err := p.generate(ctx, buildResolvePrompt(a, b), "")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(resp), "true"), nil
}

func (p *OllamaProvider) generate(ctx context.Context, prompt, format string) (string, error) {
	var resp ollamaGenerateResponse
	err := p.client.DoJSON(ctx, p.baseURL+"/api/generate", nil, ollamaGenerateRequest{
		Model:  p.model,
		Prompt: prompt,
		Stream: false,
		Format: format,
	}, &resp)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	return resp.Response, nil
}
