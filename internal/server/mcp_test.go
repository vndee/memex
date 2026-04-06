package server

import (
	"context"
	"strings"
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
		"memex_kb_get": false, "memex_kb_delete": false,
		"memex_store": false, "memex_search": false,
		"memex_entities": false, "memex_relations": false,
		"memex_delete": false, "memex_stats": false,
		"memex_lifecycle_decay": false, "memex_lifecycle_prune": false,
		"memex_lifecycle_consolidate": false,
		"memex_job_list": false, "memex_job_get": false,
		"memex_job_retry": false,
		"memex_feedback_record": false, "memex_feedback_search": false,
		"memex_feedback_stats": false,
		"memex_episode_list": false, "memex_episode_get": false,
		"memex_entity_get": false, "memex_relation_get": false,
		"memex_community_list": false,
		"memex_graph_traverse": false,
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

	if len(result.Tools) != 25 {
		t.Errorf("expected 25 tools, got %d", len(result.Tools))
	}
}

// seedGraphTestData creates a KB with entities and relations for graph-related tests.
// Returns entity IDs: e1 (Alice), e2 (Bob), e3 (Project Atlas), e4 (Carol).
// Relations: e1->e2 (knows), e1->e3 (works_on), e2->e3 (works_on), e2->e4 (knows).
func seedGraphTestData(t *testing.T, ctx context.Context, store *storage.SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   now,
	})

	entities := []*domain.Entity{
		{ID: "e1", KBID: "test", Name: "Alice", Type: "person", Summary: "Software engineer", Embedding: []float32{1, 0, 0, 0}, CreatedAt: now, UpdatedAt: now},
		{ID: "e2", KBID: "test", Name: "Bob", Type: "person", Summary: "Project manager", Embedding: []float32{0, 1, 0, 0}, CreatedAt: now, UpdatedAt: now},
		{ID: "e3", KBID: "test", Name: "Project Atlas", Type: "project", Summary: "Internal platform", Embedding: []float32{0, 0, 1, 0}, CreatedAt: now, UpdatedAt: now},
		{ID: "e4", KBID: "test", Name: "Carol", Type: "person", Summary: "Designer", Embedding: []float32{0, 0, 0, 1}, CreatedAt: now, UpdatedAt: now},
	}
	for _, e := range entities {
		if err := store.CreateEntity(ctx, e); err != nil {
			t.Fatalf("create entity %s: %v", e.ID, err)
		}
	}

	past := now.Add(-30 * 24 * time.Hour)
	relations := []*domain.Relation{
		{ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2", Type: "knows", Summary: "Alice knows Bob", Weight: 0.9, ValidAt: past, CreatedAt: now},
		{ID: "r2", KBID: "test", SourceID: "e1", TargetID: "e3", Type: "works_on", Summary: "Alice works on Atlas", Weight: 0.85, ValidAt: past, CreatedAt: now},
		{ID: "r3", KBID: "test", SourceID: "e2", TargetID: "e3", Type: "works_on", Summary: "Bob works on Atlas", Weight: 0.7, ValidAt: past, CreatedAt: now},
		{ID: "r4", KBID: "test", SourceID: "e2", TargetID: "e4", Type: "knows", Summary: "Bob knows Carol", Weight: 0.6, ValidAt: past, CreatedAt: now},
	}
	for _, r := range relations {
		if err := store.CreateRelation(ctx, r); err != nil {
			t.Fatalf("create relation %s: %v", r.ID, err)
		}
	}
}

func TestMCP_GraphTraverse_JSON(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_graph_traverse",
		Arguments: map[string]any{
			"kb":        "test",
			"entity_id": "e1",
			"hops":      2,
		},
	})
	if err != nil {
		t.Fatalf("graph_traverse: %v", err)
	}
	if result.IsError {
		t.Fatalf("graph_traverse error: %v", result.Content)
	}

	// The structured output includes a subgraph with nodes and edges.
	// Verify we got JSON output (not plain text error).
	text := result.Content[0].(*mcp.TextContent).Text
	if text == "No graph data available for this KB." {
		t.Fatal("graph should have data after seeding relations")
	}
}

func TestMCP_GraphTraverse_TextFormat(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_graph_traverse",
		Arguments: map[string]any{
			"kb":        "test",
			"entity_id": "e1",
			"hops":      2,
			"format":    "text",
		},
	})
	if err != nil {
		t.Fatalf("graph_traverse text: %v", err)
	}
	if result.IsError {
		t.Fatalf("graph_traverse text error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "Context from knowledge graph") {
		t.Errorf("expected text summary header, got: %s", text)
	}
	if !strings.Contains(text, "Alice") {
		t.Errorf("expected Alice in text output, got: %s", text)
	}
}

func TestMCP_GraphTraverse_EdgeTypeFilter(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	// Only traverse "knows" edges from e1 — should reach e2 but NOT e3 (works_on).
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_graph_traverse",
		Arguments: map[string]any{
			"kb":         "test",
			"entity_id":  "e1",
			"hops":       1,
			"edge_types": "knows",
			"format":     "text",
		},
	})
	if err != nil {
		t.Fatalf("graph_traverse edge filter: %v", err)
	}
	if result.IsError {
		t.Fatalf("graph_traverse edge filter error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "Bob") {
		t.Errorf("expected Bob (knows edge), got: %s", text)
	}
	if strings.Contains(text, "Project Atlas") {
		t.Errorf("should NOT contain Project Atlas (works_on filtered out), got: %s", text)
	}
}

func TestMCP_GraphTraverse_MissingKB(t *testing.T) {
	cs, _ := setupMCPClient(t)
	ctx := context.Background()

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_graph_traverse",
		Arguments: map[string]any{
			"entity_id": "e1",
		},
	})
	if err != nil {
		return
	}
	if !result.IsError {
		t.Fatal("expected error for missing kb param")
	}
}

func TestMCP_GraphTraverse_MissingEntityID(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_graph_traverse",
		Arguments: map[string]any{
			"kb": "test",
		},
	})
	if err != nil {
		return
	}
	if !result.IsError {
		t.Fatal("expected error for missing entity_id param")
	}
}

func TestMCP_SearchWithMaxHops(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	// Search with max_hops=1 (shallow) — should not error.
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":       "test",
			"query":    "Alice engineer",
			"mode":     "bm25",
			"max_hops": 1,
		},
	})
	if err != nil {
		t.Fatalf("search with max_hops: %v", err)
	}
	if result.IsError {
		t.Fatalf("search with max_hops error: %v", result.Content)
	}
}

func TestMCP_SearchWithGraphScorer(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	for _, scorer := range []string{"bfs", "pagerank", "weighted"} {
		t.Run(scorer, func(t *testing.T) {
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{
				Name: "memex_search",
				Arguments: map[string]any{
					"kb":           "test",
					"query":        "Alice",
					"mode":         "bm25",
					"graph_scorer": scorer,
				},
			})
			if err != nil {
				t.Fatalf("search with graph_scorer=%s: %v", scorer, err)
			}
			if result.IsError {
				t.Fatalf("search with graph_scorer=%s error: %v", scorer, result.Content)
			}
		})
	}
}

func TestMCP_SearchWithGraphScorer_Invalid(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":           "test",
			"query":        "test",
			"graph_scorer": "invalid_scorer",
		},
	})
	if err != nil {
		// Transport error is also acceptable.
		return
	}
	if !result.IsError {
		t.Fatal("expected error for invalid graph_scorer")
	}
}

func TestMCP_SearchWithEdgeTypes(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":         "test",
			"query":      "Alice",
			"mode":       "bm25",
			"edge_types": "knows",
		},
	})
	if err != nil {
		t.Fatalf("search with edge_types: %v", err)
	}
	if result.IsError {
		t.Fatalf("search with edge_types error: %v", result.Content)
	}
}

func TestMCP_SearchWithMinWeight(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":           "test",
			"query":        "Alice",
			"mode":         "bm25",
			"graph_scorer": "weighted",
			"min_weight":   0.8,
		},
	})
	if err != nil {
		t.Fatalf("search with min_weight: %v", err)
	}
	if result.IsError {
		t.Fatalf("search with min_weight error: %v", result.Content)
	}
}

func TestMCP_SearchWithTemporalAt(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":    "test",
			"query": "Alice",
			"mode":  "bm25",
			"at":    time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("search with at: %v", err)
	}
	if result.IsError {
		t.Fatalf("search with at error: %v", result.Content)
	}
}

func TestMCP_SearchWithTemporalAt_Invalid(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":    "test",
			"query": "test",
			"at":    "not-a-timestamp",
		},
	})
	if err != nil {
		return
	}
	if !result.IsError {
		t.Fatal("expected error for invalid at timestamp")
	}
}

func TestMCP_SearchWithExpandCommunities(t *testing.T) {
	cs, store := setupMCPClient(t)
	ctx := context.Background()
	seedGraphTestData(t, ctx, store)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memex_search",
		Arguments: map[string]any{
			"kb":                 "test",
			"query":              "Alice",
			"mode":               "bm25",
			"expand_communities": true,
		},
	})
	if err != nil {
		t.Fatalf("search with expand_communities: %v", err)
	}
	if result.IsError {
		t.Fatalf("search with expand_communities error: %v", result.Content)
	}
}
