package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vndee/memex/internal/httpclient"
)

// OpenAIProvider uses an OpenAI-compatible chat completions endpoint for extraction.
// Works with OpenAI, GenAI (Gemini), Vertex AI, Groq, etc.
//
// The baseURL should include the API version prefix:
//   - OpenAI:    "https://api.openai.com/v1"
//   - GenAI:     "https://generativelanguage.googleapis.com/v1beta/openai"
//   - Vertex AI: "https://{LOCATION}-aiplatform.googleapis.com/v1beta1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi"
type OpenAIProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *httpclient.Client
}

// NewOpenAIProvider creates an OpenAI-compatible LLM extraction provider.
func NewOpenAIProvider(baseURL, model, apiKey string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  httpclient.New(),
	}
}

// NewOpenAIProviderWithClient creates a provider with a custom HTTP client
// (e.g., one with OAuth2 transport for Vertex AI ADC).
func NewOpenAIProviderWithClient(baseURL, model, apiKey string, client *httpclient.Client) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  client,
	}
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Extract entities and relations from the given text.
func (p *OpenAIProvider) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	resp, err := p.chat(ctx, buildExtractionPrompt(text), &respFormat{Type: "json_object"})
	if err != nil {
		return nil, fmt.Errorf("openai extract: %w", err)
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("openai extract: parse response: %w (raw: %s)", err, truncate(resp, 200))
	}

	normalizeResult(&result)
	return &result, nil
}

// Summarize produces a concise summary of the given text.
func (p *OpenAIProvider) Summarize(ctx context.Context, text string) (string, error) {
	return p.chat(ctx, buildSummarizePrompt(text), nil)
}

// ResolveEntity determines if two entity descriptions refer to the same entity.
func (p *OpenAIProvider) ResolveEntity(ctx context.Context, a, b ExtractedEntity) (bool, error) {
	resp, err := p.chat(ctx, buildResolvePrompt(a, b), nil)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(resp), "true"), nil
}

func (p *OpenAIProvider) chat(ctx context.Context, prompt string, format *respFormat) (string, error) {
	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	var resp chatResponse
	err := p.client.DoJSON(ctx, p.baseURL+"/chat/completions", headers, chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		ResponseFormat: format,
	}, &resp)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response from API")
	}

	return resp.Choices[0].Message.Content, nil
}
