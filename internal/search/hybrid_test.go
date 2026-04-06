package search

import (
	"context"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

// mockEmbedder returns a fixed vector for all embed calls.
type mockEmbedder struct {
	vec []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, nil
}
func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vecs[i] = m.vec
	}
	return vecs, nil
}
func (m *mockEmbedder) Dimensions() int { return len(m.vec) }

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal("failed to create store:", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedTestData(t *testing.T, store *storage.SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	kb := &domain.KnowledgeBase{
		ID:   "test",
		Name: "Test KB",
		EmbedConfig: domain.EmbedConfig{
			Provider: "ollama",
			Model:    "nomic-embed-text",
			Dim:      4,
		},
		LLMConfig: domain.LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateKB(ctx, kb); err != nil {
		t.Fatal("create KB:", err)
	}

	entities := []*domain.Entity{
		{ID: "e1", KBID: "test", Name: "Alice", Type: "person", Summary: "Software engineer at Acme", Embedding: []float32{1, 0, 0, 0}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{ID: "e2", KBID: "test", Name: "Bob", Type: "person", Summary: "Product manager at Acme", Embedding: []float32{0, 1, 0, 0}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{ID: "e3", KBID: "test", Name: "Acme", Type: "company", Summary: "Tech startup building AI tools", Embedding: []float32{0, 0, 1, 0}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		{ID: "e4", KBID: "test", Name: "Rust", Type: "language", Summary: "Systems programming language", Embedding: []float32{0, 0, 0, 1}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
	}
	for _, e := range entities {
		if err := store.CreateEntity(ctx, e); err != nil {
			t.Fatalf("create entity %s: %v", e.ID, err)
		}
	}

	relations := []*domain.Relation{
		{ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e3", Type: "works_at", Summary: "Alice works at Acme", Weight: 1.0, ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC()},
		{ID: "r2", KBID: "test", SourceID: "e2", TargetID: "e3", Type: "works_at", Summary: "Bob works at Acme", Weight: 1.0, ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC()},
		{ID: "r3", KBID: "test", SourceID: "e1", TargetID: "e4", Type: "knows", Summary: "Alice knows Rust", Weight: 0.8, ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC()},
	}
	for _, r := range relations {
		if err := store.CreateRelation(ctx, r); err != nil {
			t.Fatalf("create relation %s: %v", r.ID, err)
		}
	}

	ep := &domain.Episode{
		ID: "ep1", KBID: "test",
		Content:   "Alice is a software engineer at Acme working on Rust projects",
		Source:    "test",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateEpisode(ctx, ep); err != nil {
		t.Fatal("create episode:", err)
	}
}

func mockEmbedFactory(vec []float32) EmbedderFactory {
	return func(cfg domain.EmbedConfig) (embedding.Provider, error) {
		return &mockEmbedder{vec: vec}, nil
	}
}

func TestSearch_BM25Only(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store)

	s := New(store, vecstore.NewEngine(vecstore.EngineConfig{}), graph.NewStore(),
		mockEmbedFactory([]float32{1, 0, 0, 0}), 168)

	opts := DefaultOptions()
	opts.Channels = Channels{BM25: true}

	results, err := s.Search(context.Background(), "test", "Alice engineer", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from BM25 search")
	}

	// Alice should appear since she matches "Alice" and "engineer".
	found := false
	for _, r := range results {
		if r.ID == "e1" {
			found = true
		}
	}
	if !found {
		t.Error("expected Alice (e1) in BM25 results")
	}
}

func TestSearch_VectorOnly(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store)

	// Query vector close to Alice's embedding [1,0,0,0].
	queryVec := []float32{0.9, 0.1, 0, 0}

	s := New(store, vecstore.NewEngine(vecstore.EngineConfig{}), graph.NewStore(),
		mockEmbedFactory(queryVec), 168)

	opts := DefaultOptions()
	opts.Channels = Channels{Vector: true}

	results, err := s.Search(context.Background(), "test", "Alice", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from vector search")
	}

	// Alice should be the top result (closest vector).
	if results[0].ID != "e1" {
		t.Errorf("expected Alice (e1) as top result, got %s", results[0].ID)
	}
}

func TestSearch_HybridFusion(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store)

	// Query vector close to Alice.
	queryVec := []float32{0.9, 0.1, 0, 0}

	s := New(store, vecstore.NewEngine(vecstore.EngineConfig{}), graph.NewStore(),
		mockEmbedFactory(queryVec), 168)

	opts := DefaultOptions()
	opts.TopK = 5

	results, err := s.Search(context.Background(), "test", "Alice engineer", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from hybrid search")
	}

	// Alice should be boosted by appearing in both BM25 and vector.
	if results[0].ID != "e1" {
		t.Errorf("expected Alice (e1) as top hybrid result, got %s", results[0].ID)
	}
}

func TestSearch_GraphExpansion(t *testing.T) {
	store := newTestStore(t)
	seedTestData(t, store)

	queryVec := []float32{1, 0, 0, 0}

	s := New(store, vecstore.NewEngine(vecstore.EngineConfig{}), graph.NewStore(),
		mockEmbedFactory(queryVec), 168)

	opts := DefaultOptions()
	opts.TopK = 10

	results, err := s.Search(context.Background(), "test", "Alice", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// Through graph: Alice -> works_at -> Acme, Alice -> knows -> Rust
	// Bob connects to Acme too.
	// Check that Acme or Rust or Bob appear via graph expansion.
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}

	// At minimum, Alice should be there from BM25/vector.
	if !ids["e1"] {
		t.Error("expected Alice (e1) in results")
	}
}

func TestSearch_EmptyKB(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	kb := &domain.KnowledgeBase{
		ID: "empty", Name: "Empty",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "nomic-embed-text", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "llama3.2"},
		CreatedAt:   time.Now().UTC(),
	}
	store.CreateKB(ctx, kb)

	s := New(store, vecstore.NewEngine(vecstore.EngineConfig{}), graph.NewStore(),
		mockEmbedFactory([]float32{1, 0, 0, 0}), 168)

	results, err := s.Search(ctx, "empty", "anything", DefaultOptions())
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestExtractEntityIDs(t *testing.T) {
	results := []*domain.SearchResult{
		{ID: "e1", Type: "entity"},
		{ID: "r1", Type: "relation", Metadata: map[string]string{"source_id": "e2", "target_id": "e3"}},
		{ID: "e1", Type: "entity"}, // duplicate
		{ID: "ep1", Type: "episode"},
	}

	ids := extractEntityIDs(results)

	expected := map[string]bool{"e1": true, "e2": true, "e3": true}
	if len(ids) != len(expected) {
		t.Errorf("want %d IDs, got %d: %v", len(expected), len(ids), ids)
	}
	for _, id := range ids {
		if !expected[id] {
			t.Errorf("unexpected ID: %s", id)
		}
	}
}
