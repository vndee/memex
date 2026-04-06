package server

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/lifecycle"
	"github.com/vndee/memex/internal/notify"
	"github.com/vndee/memex/internal/search"
	"github.com/vndee/memex/internal/storage"
)

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

// mockEmbedder returns a fixed vector.
type mockEmbedder struct{ vec []float32 }

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

func mockEmbedFactory(vec []float32) search.EmbedderFactory {
	return func(cfg domain.EmbedConfig) (embedding.Provider, error) {
		return &mockEmbedder{vec: vec}, nil
	}
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func setupMCPClient(t *testing.T) (*mcp.ClientSession, *storage.SQLiteStore) {
	t.Helper()
	store := newTestStore(t)

	pipe := ingestion.NewPipeline(store, embedding.NewRegistry(), extraction.NewRegistry())
	sched := ingestion.NewScheduler(pipe, store, ingestion.SchedulerConfig{
		PoolSize:    1,
		MaxAttempts: 1,
		Async:       false,
	}, notify.LogNotifier{})

	searcher := NewSearcher(store, 168, mockEmbedFactory([]float32{1, 0, 0, 0}))
	lcManager := lifecycle.NewManager(store, lifecycle.ManagerConfig{})
	consolidator := lifecycle.NewConsolidator(store, 0)
	mcpSrv := NewMCPServer(store, sched, searcher, lcManager, consolidator, "test")

	// Use in-memory transports for testing.
	serverT, clientT := mcp.NewInMemoryTransports()

	ctx := context.Background()
	_, err := mcpSrv.server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal("server connect:", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal("client connect:", err)
	}

	t.Cleanup(func() { cs.Close() })
	return cs, store
}

func TestMCP_KBCreateAndList(t *testing.T) {
	cs, _ := setupMCPClient(t)
	ctx := context.Background()

	// Create a KB.
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_kb_create",
		Arguments: map[string]any{
			"id":   "test-kb",
			"name": "Test Knowledge Base",
		},
	})
	if err != nil {
		t.Fatalf("kb_create: %v", err)
	}
	if result.IsError {
		t.Fatalf("kb_create returned error: %v", result.Content)
	}

	// List KBs.
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_kb_list",
	})
	if err != nil {
		t.Fatalf("kb_list: %v", err)
	}
	if result.IsError {
		t.Fatalf("kb_list returned error: %v", result.Content)
	}
	// Should contain our KB.
	text := result.Content[0].(*mcp.TextContent).Text
	if text == "No knowledge bases found." {
		t.Error("expected at least one KB in list")
	}
}

func TestMCP_Stats(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	// Create KB first.
	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memex_stats",
		Arguments: map[string]any{"kb": "test"},
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if result.IsError {
		t.Fatalf("stats returned error: %v", result.Content)
	}
}

func TestMCP_EntitiesAndRelations(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	// Seed data.
	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "Alice knows Bob", Weight: 1.0,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	// List entities.
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memex_entities",
		Arguments: map[string]any{"kb": "test"},
	})
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	if result.IsError {
		t.Fatalf("entities error: %v", result.Content)
	}

	// Get relations for entity.
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memex_relations",
		Arguments: map[string]any{"kb": "test", "entity_id": "e1"},
	})
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	if result.IsError {
		t.Fatalf("relations error: %v", result.Content)
	}
}

func TestMCP_Search(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	// Seed data.
	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Software engineer at Acme", Embedding: []float32{1, 0, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// BM25 search.
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":    "test",
			"query": "Alice engineer",
			"mode":  "bm25",
		},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.IsError {
		t.Fatalf("search error: %v", result.Content)
	}
}

func TestMCP_Delete(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_delete",
		Arguments: map[string]any{
			"kb":   "test",
			"id":   "e1",
			"type": "entity",
		},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if result.IsError {
		t.Fatalf("delete error: %v", result.Content)
	}

	// Verify entity is gone.
	_, err = store.GetEntity(ctx, "test", "e1")
	if err == nil {
		t.Error("entity should be deleted")
	}
}

func TestMCP_ListTools(t *testing.T) {
	cs, _ := setupMCPClient(t)
	ctx := context.Background()

	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	expectedTools := map[string]bool{
		"memex_kb_create": false, "memex_kb_list": false,
		"memex_store": false, "memex_search": false,
		"memex_entities": false, "memex_relations": false,
		"memex_delete": false, "memex_stats": false,
		"memex_lifecycle_decay": false, "memex_lifecycle_prune": false,
		"memex_lifecycle_consolidate": false,
	}

	for _, tool := range result.Tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}

	if len(result.Tools) != 11 {
		t.Errorf("expected 11 tools, got %d", len(result.Tools))
	}
}
