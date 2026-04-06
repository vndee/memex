package search

import (
	"context"
	"fmt"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

type countingSearchStore struct {
	storage.Store
	getEntityCalls              int
	getEntitiesByIDsCalls       int
	getEntitiesByIDsBatchSizes  []int
}

func (s *countingSearchStore) GetEntity(ctx context.Context, kbID, id string) (*domain.Entity, error) {
	s.getEntityCalls++
	return s.Store.GetEntity(ctx, kbID, id)
}

func (s *countingSearchStore) GetEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]*domain.Entity, error) {
	s.getEntitiesByIDsCalls++
	s.getEntitiesByIDsBatchSizes = append(s.getEntitiesByIDsBatchSizes, len(ids))
	return s.Store.GetEntitiesByIDs(ctx, kbID, ids)
}

func TestHydrateEntityResults_UsesBatchLookup(t *testing.T) {
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

	store := &countingSearchStore{Store: sqliteStore}
	ids := []string{"e1", "missing", "e2"}
	scores := map[string]float64{"e1": 1, "e2": 0.5, "missing": 0.1}

	results, err := hydrateEntityResults(context.Background(), store, "kb1", ids, scores, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if store.getEntitiesByIDsCalls != 1 {
		t.Fatalf("GetEntitiesByIDs calls = %d, want 1", store.getEntitiesByIDsCalls)
	}
	if store.getEntityCalls != 0 {
		t.Fatalf("GetEntity calls = %d, want 0", store.getEntityCalls)
	}
}

func TestHydrateEntityResults_DoesNotFetchAllIDsWhenLimitSmall(t *testing.T) {
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
		ID: "e001", KBID: "kb1", Name: "Alice", Type: "person", Summary: "Engineer", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	store := &countingSearchStore{Store: sqliteStore}
	ids := make([]string, 0, 200)
	scores := make(map[string]float64, 200)
	for i := 1; i <= 200; i++ {
		id := fmt.Sprintf("missing-%03d", i)
		if i == 1 {
			id = "e001"
		}
		ids = append(ids, id)
		scores[id] = 1
	}

	results, err := hydrateEntityResults(context.Background(), store, "kb1", ids, scores, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if store.getEntitiesByIDsCalls != 1 {
		t.Fatalf("GetEntitiesByIDs calls = %d, want 1", store.getEntitiesByIDsCalls)
	}
	if len(store.getEntitiesByIDsBatchSizes) != 1 {
		t.Fatalf("batch size captures = %d, want 1", len(store.getEntitiesByIDsBatchSizes))
	}
	if store.getEntitiesByIDsBatchSizes[0] >= len(ids) {
		t.Fatalf("batch fetched %d IDs, expected less than total %d", store.getEntitiesByIDsBatchSizes[0], len(ids))
	}
}
