package server

import (
	"time"

	"github.com/vndee/memex/internal/domain"
)

// buildKB constructs a KnowledgeBase with defaults applied for empty fields.
// Shared by both HTTP and MCP handlers.
func buildKB(id, name, desc, embedProvider, embedModel, llmProvider, llmModel string) *domain.KnowledgeBase {
	if name == "" {
		name = id
	}
	if embedProvider == "" {
		embedProvider = domain.ProviderOllama
	}
	if embedModel == "" {
		embedModel = domain.DefaultEmbedModel
	}
	if llmProvider == "" {
		llmProvider = domain.ProviderOllama
	}
	if llmModel == "" {
		llmModel = domain.DefaultLLMModel
	}

	return &domain.KnowledgeBase{
		ID:          id,
		Name:        name,
		Description: desc,
		EmbedConfig: domain.EmbedConfig{
			Provider: embedProvider,
			Model:    embedModel,
		},
		LLMConfig: domain.LLMConfig{
			Provider: llmProvider,
			Model:    llmModel,
		},
		CreatedAt: time.Now().UTC(),
	}
}
