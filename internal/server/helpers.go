package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
)

type subgraphMetadataLoader interface {
	GetSubgraphEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]storage.SubgraphEntityMetadata, error)
	GetSubgraphRelationsByIDs(ctx context.Context, kbID string, ids []string) (map[string]storage.SubgraphRelationMetadata, error)
}

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
	nodeIndex := make(map[string]int, len(sg.Nodes))
	nodeIDs := make([]string, 0, len(sg.Nodes))
	nodes := make([]domain.SubgraphNode, 0, len(sg.Nodes))
	for id := range sg.Nodes {
		nodeIndex[id] = len(nodes)
		nodeIDs = append(nodeIDs, id)
		nodes = append(nodes, domain.SubgraphNode{
			ID:       id,
			Distance: sg.Nodes[id],
		})
	}

	edgeIndex := make(map[string]int, len(sg.Edges))
	edgeIDs := make([]string, 0, len(sg.Edges))
	edges := make([]domain.SubgraphEdge, 0, len(sg.Edges))
	for _, e := range sg.Edges {
		edgeIndex[e.RelID] = len(edges)
		edgeIDs = append(edgeIDs, e.RelID)
		edges = append(edges, domain.SubgraphEdge{
			ID:       e.RelID,
			SourceID: e.SourceID,
			TargetID: e.TargetID,
			Type:     e.Type,
			Weight:   e.Weight,
		})
	}

	if loader, ok := store.(subgraphMetadataLoader); ok {
		entitiesByID, err := loader.GetSubgraphEntitiesByIDs(ctx, kbID, nodeIDs)
		if err != nil {
			slog.Warn("subgraph entity hydration failed", "count", len(nodeIDs), "error", err)
		} else {
			for id, ent := range entitiesByID {
				idx, ok := nodeIndex[id]
				if !ok {
					continue
				}
				nodes[idx].ID = ent.ID
				nodes[idx].Name = ent.Name
				nodes[idx].Type = ent.Type
				nodes[idx].Summary = ent.Summary
			}
		}

		relationsByID, err := loader.GetSubgraphRelationsByIDs(ctx, kbID, edgeIDs)
		if err != nil {
			slog.Warn("subgraph relation hydration failed", "count", len(edgeIDs), "error", err)
		} else {
			for id, rel := range relationsByID {
				idx, ok := edgeIndex[id]
				if !ok {
					continue
				}
				edges[idx].ID = rel.ID
				edges[idx].SourceID = rel.SourceID
				edges[idx].TargetID = rel.TargetID
				edges[idx].Type = rel.Type
				edges[idx].Weight = rel.Weight
				edges[idx].ValidAt = rel.ValidAt
				edges[idx].InvalidAt = rel.InvalidAt
			}
		}

		return &domain.Subgraph{Nodes: nodes, Edges: edges}, nil
	}

	for i := range nodes {
		ent, err := store.GetEntity(ctx, kbID, nodes[i].ID)
		if err != nil {
			continue
		}
		nodes[i].ID = ent.ID
		nodes[i].Name = ent.Name
		nodes[i].Type = ent.Type
		nodes[i].Summary = ent.Summary
	}

	for i := range edges {
		rel, err := store.GetRelation(ctx, kbID, edges[i].ID)
		if err != nil {
			continue
		}
		edges[i].ID = rel.ID
		edges[i].SourceID = rel.SourceID
		edges[i].TargetID = rel.TargetID
		edges[i].Type = rel.Type
		edges[i].Weight = rel.Weight
		edges[i].ValidAt = rel.ValidAt
		edges[i].InvalidAt = rel.InvalidAt
	}

	return &domain.Subgraph{Nodes: nodes, Edges: edges}, nil
}
