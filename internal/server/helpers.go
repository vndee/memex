package server

import (
	"context"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
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

// HydrateSubgraph enriches a raw SubgraphResult with entity and relation metadata
// from storage. Used by MCP, HTTP, and CLI graph traversal handlers.
func HydrateSubgraph(ctx context.Context, store storage.Store, kbID string, sg graph.SubgraphResult) (*domain.Subgraph, error) {
	nodeIDs := make([]string, 0, len(sg.Nodes))
	for id := range sg.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	entitiesByID, err := store.GetEntitiesByIDs(ctx, kbID, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("get subgraph entities by ids: %w", err)
	}

	nodes := make([]domain.SubgraphNode, 0, len(sg.Nodes))
	for id, dist := range sg.Nodes {
		ent, ok := entitiesByID[id]
		if !ok {
			nodes = append(nodes, domain.SubgraphNode{
				ID: id, Distance: dist,
			})
			continue
		}
		nodes = append(nodes, domain.SubgraphNode{
			ID:       ent.ID,
			Name:     ent.Name,
			Type:     ent.Type,
			Summary:  ent.Summary,
			Distance: dist,
		})
	}

	edgeIDs := make([]string, 0, len(sg.Edges))
	for _, e := range sg.Edges {
		edgeIDs = append(edgeIDs, e.RelID)
	}
	relationsByID, err := store.GetRelationsByIDs(ctx, kbID, edgeIDs)
	if err != nil {
		return nil, fmt.Errorf("get subgraph relations by ids: %w", err)
	}

	edges := make([]domain.SubgraphEdge, 0, len(sg.Edges))
	for _, e := range sg.Edges {
		rel, ok := relationsByID[e.RelID]
		if !ok {
			edges = append(edges, domain.SubgraphEdge{
				ID:       e.RelID,
				SourceID: e.SourceID,
				TargetID: e.TargetID,
				Type:     e.Type,
				Weight:   e.Weight,
			})
			continue
		}
		edges = append(edges, domain.SubgraphEdge{
			ID:        rel.ID,
			SourceID:  rel.SourceID,
			TargetID:  rel.TargetID,
			Type:      rel.Type,
			Weight:    rel.Weight,
			ValidAt:   rel.ValidAt,
			InvalidAt: rel.InvalidAt,
		})
	}

	return &domain.Subgraph{Nodes: nodes, Edges: edges}, nil
}
