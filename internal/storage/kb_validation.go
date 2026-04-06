package storage

import (
	"fmt"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
)

func validateKBProviders(kb *domain.KnowledgeBase) error {
	if kb == nil {
		return fmt.Errorf("knowledge base is nil")
	}

	if _, err := embedding.NewRegistry().NewProvider(kb.EmbedConfig); err != nil {
		return fmt.Errorf("invalid embedding config: %w", err)
	}
	if _, err := extraction.NewRegistry().NewProvider(kb.LLMConfig); err != nil {
		return fmt.Errorf("invalid LLM config: %w", err)
	}

	return nil
}
