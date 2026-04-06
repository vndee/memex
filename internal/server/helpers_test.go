package server

import (
	"context"
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
	getEntityCalls            int
	getEntitiesByIDsCalls     int
	getRelationCalls          int
	getRelationsByIDsCalls    int
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

func TestHydrateSubgraph_UsesBatchLookups(t *testing.T) {
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
	if store.getEntitiesByIDsCalls != 1 {
		t.Fatalf("GetEntitiesByIDs calls = %d, want 1", store.getEntitiesByIDsCalls)
	}
	if store.getRelationsByIDsCalls != 1 {
		t.Fatalf("GetRelationsByIDs calls = %d, want 1", store.getRelationsByIDsCalls)
	}
	if store.getEntityCalls != 0 {
		t.Fatalf("GetEntity calls = %d, want 0", store.getEntityCalls)
	}
	if store.getRelationCalls != 0 {
		t.Fatalf("GetRelation calls = %d, want 0", store.getRelationCalls)
	}
}

