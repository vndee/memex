package server

import (
	"context"
	"errors"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
)

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

type countingServerStore struct {
	storage.Store
	getEntityCalls                 int
	getEntitiesByIDsCalls          int
	getRelationCalls               int
	getRelationsByIDsCalls         int
	getSubgraphEntitiesByIDsCalls  int
	getSubgraphRelationsByIDsCalls int
	failSubgraphEntitiesByIDs      bool
	failSubgraphRelationsByIDs     bool
}

func (s *countingServerStore) GetEntity(ctx context.Context, kbID, id string) (*domain.Entity, error) {
	s.getEntityCalls++
	return s.Store.GetEntity(ctx, kbID, id)
}

func (s *countingServerStore) GetEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]*domain.Entity, error) {
	s.getEntitiesByIDsCalls++
	return s.Store.GetEntitiesByIDs(ctx, kbID, ids)
}

func (s *countingServerStore) GetRelation(ctx context.Context, kbID, id string) (*domain.Relation, error) {
	s.getRelationCalls++
	return s.Store.GetRelation(ctx, kbID, id)
}

func (s *countingServerStore) GetRelationsByIDs(ctx context.Context, kbID string, ids []string) (map[string]*domain.Relation, error) {
	s.getRelationsByIDsCalls++
	return s.Store.GetRelationsByIDs(ctx, kbID, ids)
}

func (s *countingServerStore) GetSubgraphEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]storage.SubgraphEntityMetadata, error) {
	s.getSubgraphEntitiesByIDsCalls++
	if s.failSubgraphEntitiesByIDs {
		return nil, errors.New("entity hydration failed")
	}
	loader, ok := s.Store.(interface {
		GetSubgraphEntitiesByIDs(context.Context, string, []string) (map[string]storage.SubgraphEntityMetadata, error)
	})
	if !ok {
		return nil, errors.New("subgraph entity loader not supported")
	}
	return loader.GetSubgraphEntitiesByIDs(ctx, kbID, ids)
}

func (s *countingServerStore) GetSubgraphRelationsByIDs(ctx context.Context, kbID string, ids []string) (map[string]storage.SubgraphRelationMetadata, error) {
	s.getSubgraphRelationsByIDsCalls++
	if s.failSubgraphRelationsByIDs {
		return nil, errors.New("relation hydration failed")
	}
	loader, ok := s.Store.(interface {
		GetSubgraphRelationsByIDs(context.Context, string, []string) (map[string]storage.SubgraphRelationMetadata, error)
	})
	if !ok {
		return nil, errors.New("subgraph relation loader not supported")
	}
	return loader.GetSubgraphRelationsByIDs(ctx, kbID, ids)
}

func TestHydrateSubgraph_UsesLightweightBatchLookups(t *testing.T) {
	sqliteStore, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqliteStore.Close() })

	now := time.Now().UTC()
	if err := sqliteStore.CreateKB(context.Background(), &domain.KnowledgeBase{
		ID: "kb1", Name: "KB 1",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "nomic-embed-text"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "llama3.2"},
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*domain.Entity{
		{ID: "e1", KBID: "kb1", Name: "Alice", Type: "person", Summary: "Engineer", CreatedAt: now, UpdatedAt: now},
		{ID: "e2", KBID: "kb1", Name: "Bob", Type: "person", Summary: "Manager", CreatedAt: now, UpdatedAt: now},
	} {
		if err := sqliteStore.CreateEntity(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if err := sqliteStore.CreateRelation(context.Background(), &domain.Relation{
		ID: "r1", KBID: "kb1", SourceID: "e1", TargetID: "e2", Type: "knows", Weight: 1.0, ValidAt: now, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	store := &countingServerStore{Store: sqliteStore}
	subgraph := graph.SubgraphResult{
		Nodes: map[string]int{"e1": 0, "e2": 1, "missing": 2},
		Edges: []graph.SubgraphEdge{
			{RelID: "r1", SourceID: "e1", TargetID: "e2", Type: "knows", Weight: 1},
			{RelID: "missing-rel", SourceID: "e2", TargetID: "missing", Type: "mentions", Weight: 0.5},
		},
	}

	hydrated, err := HydrateSubgraph(context.Background(), store, "kb1", subgraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(hydrated.Nodes))
	}
	if len(hydrated.Edges) != 2 {
		t.Fatalf("got %d edges, want 2", len(hydrated.Edges))
	}
	nodesByID := make(map[string]domain.SubgraphNode, len(hydrated.Nodes))
	for _, n := range hydrated.Nodes {
		nodesByID[n.ID] = n
	}
	if nodesByID["e1"].Name != "Alice" || nodesByID["e1"].Summary != "Engineer" {
		t.Fatalf("expected hydrated node for e1, got %+v", nodesByID["e1"])
	}
	if nodesByID["e2"].Name != "Bob" || nodesByID["e2"].Summary != "Manager" {
		t.Fatalf("expected hydrated node for e2, got %+v", nodesByID["e2"])
	}
	if nodesByID["missing"].Name != "" || nodesByID["missing"].Summary != "" {
		t.Fatalf("expected fallback node for missing entity, got %+v", nodesByID["missing"])
	}

	edgesByID := make(map[string]domain.SubgraphEdge, len(hydrated.Edges))
	for _, e := range hydrated.Edges {
		edgesByID[e.ID] = e
	}
	if edgesByID["r1"].SourceID != "e1" || edgesByID["r1"].TargetID != "e2" || edgesByID["r1"].Type != "knows" {
		t.Fatalf("expected hydrated edge for r1, got %+v", edgesByID["r1"])
	}
	if edgesByID["missing-rel"].SourceID != "e2" || edgesByID["missing-rel"].TargetID != "missing" || edgesByID["missing-rel"].Type != "mentions" {
		t.Fatalf("expected fallback edge for missing-rel, got %+v", edgesByID["missing-rel"])
	}
	if store.getEntitiesByIDsCalls != 0 {
		t.Fatalf("GetEntitiesByIDs calls = %d, want 0", store.getEntitiesByIDsCalls)
	}
	if store.getRelationsByIDsCalls != 0 {
		t.Fatalf("GetRelationsByIDs calls = %d, want 0", store.getRelationsByIDsCalls)
	}
	if store.getSubgraphEntitiesByIDsCalls != 1 {
		t.Fatalf("GetSubgraphEntitiesByIDs calls = %d, want 1", store.getSubgraphEntitiesByIDsCalls)
	}
	if store.getSubgraphRelationsByIDsCalls != 1 {
		t.Fatalf("GetSubgraphRelationsByIDs calls = %d, want 1", store.getSubgraphRelationsByIDsCalls)
	}
	if store.getEntityCalls != 0 {
		t.Fatalf("GetEntity calls = %d, want 0", store.getEntityCalls)
	}
	if store.getRelationCalls != 0 {
		t.Fatalf("GetRelation calls = %d, want 0", store.getRelationCalls)
	}
}

func TestHydrateSubgraph_FallsBackWhenBatchHydrationFails(t *testing.T) {
	sqliteStore, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqliteStore.Close() })

	now := time.Now().UTC()
	if err := sqliteStore.CreateKB(context.Background(), &domain.KnowledgeBase{
		ID: "kb1", Name: "KB 1",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "nomic-embed-text"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "llama3.2"},
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqliteStore.CreateEntity(context.Background(), &domain.Entity{
		ID:        "e1",
		KBID:      "kb1",
		Name:      "Alice",
		Type:      "person",
		Summary:   "Engineer",
		Embedding: []float32{1, 2, 3},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqliteStore.CreateEntity(context.Background(), &domain.Entity{
		ID:        "missing",
		KBID:      "kb1",
		Name:      "Unhydrated",
		Type:      "person",
		Summary:   "Fallback target",
		Embedding: []float32{4, 5, 6},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqliteStore.CreateRelation(context.Background(), &domain.Relation{
		ID:        "r1",
		KBID:      "kb1",
		SourceID:  "e1",
		TargetID:  "missing",
		Type:      "knows",
		Summary:   "Alice knows someone",
		Weight:    1,
		Embedding: []float32{1, 2, 3},
		ValidAt:   now,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	store := &countingServerStore{
		Store:                      sqliteStore,
		failSubgraphEntitiesByIDs:  true,
		failSubgraphRelationsByIDs: true,
	}
	subgraph := graph.SubgraphResult{
		Nodes: map[string]int{"e1": 0, "missing": 1},
		Edges: []graph.SubgraphEdge{
			{RelID: "r1", SourceID: "e1", TargetID: "missing", Type: "knows", Weight: 1},
		},
	}

	hydrated, err := HydrateSubgraph(context.Background(), store, "kb1", subgraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(hydrated.Nodes))
	}
	if len(hydrated.Edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(hydrated.Edges))
	}

	nodesByID := make(map[string]domain.SubgraphNode, len(hydrated.Nodes))
	for _, n := range hydrated.Nodes {
		nodesByID[n.ID] = n
	}
	if nodesByID["e1"].Name != "" || nodesByID["e1"].Summary != "" {
		t.Fatalf("expected raw fallback node for e1, got %+v", nodesByID["e1"])
	}
	if nodesByID["e1"].Distance != 0 || nodesByID["missing"].Distance != 1 {
		t.Fatalf("expected fallback distances to be preserved, got %+v", hydrated.Nodes)
	}

	edge := hydrated.Edges[0]
	if edge.ID != "r1" || edge.SourceID != "e1" || edge.TargetID != "missing" || edge.Type != "knows" || edge.Weight != 1 {
		t.Fatalf("expected raw fallback edge, got %+v", edge)
	}
	if !edge.ValidAt.IsZero() || edge.InvalidAt != nil {
		t.Fatalf("expected temporal metadata to remain empty on fallback, got %+v", edge)
	}

	if store.getSubgraphEntitiesByIDsCalls != 1 {
		t.Fatalf("GetSubgraphEntitiesByIDs calls = %d, want 1", store.getSubgraphEntitiesByIDsCalls)
	}
	if store.getSubgraphRelationsByIDsCalls != 1 {
		t.Fatalf("GetSubgraphRelationsByIDs calls = %d, want 1", store.getSubgraphRelationsByIDsCalls)
	}
	if store.getEntityCalls != 0 || store.getRelationCalls != 0 {
		t.Fatalf("expected no per-item fallback queries, got GetEntity=%d GetRelation=%d", store.getEntityCalls, store.getRelationCalls)
	}
}
