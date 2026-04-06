package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/lifecycle"
	"github.com/vndee/memex/internal/notify"
	"github.com/vndee/memex/internal/storage"
)

func setupHTTPServer(t *testing.T) (*httptest.Server, *storage.SQLiteStore) {
	t.Helper()
	storage.MigrationSQL = memex.MigrationSQL()

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
	httpSrv := NewHTTPServer(store, sched, searcher, lcManager, consolidator)

	ts := httptest.NewServer(httpSrv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestHTTP_Health(t *testing.T) {
	ts, _ := setupHTTPServer(t)

	resp := doJSON(t, ts, "GET", "/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	decodeJSON(t, resp, &result)
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", result["status"])
	}
}

func TestHTTP_KBCreateAndList(t *testing.T) {
	ts, _ := setupHTTPServer(t)

	// Create KB.
	resp := doJSON(t, ts, "POST", "/api/v1/kb/", map[string]string{
		"id":   "test-kb",
		"name": "Test Knowledge Base",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", resp.StatusCode)
	}

	var kb domain.KnowledgeBase
	decodeJSON(t, resp, &kb)
	if kb.ID != "test-kb" {
		t.Errorf("expected ID=test-kb, got %q", kb.ID)
	}

	// List KBs.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}

	var listResult struct {
		KnowledgeBases []*domain.KnowledgeBase `json:"knowledge_bases"`
	}
	decodeJSON(t, resp, &listResult)
	if len(listResult.KnowledgeBases) != 1 {
		t.Errorf("expected 1 KB, got %d", len(listResult.KnowledgeBases))
	}
}

func TestHTTP_KBGetAndDelete(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	// Get KB.
	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", resp.StatusCode)
	}
	var kb domain.KnowledgeBase
	decodeJSON(t, resp, &kb)
	if kb.ID != "test" {
		t.Errorf("expected test, got %q", kb.ID)
	}

	// Delete KB.
	resp = doJSON(t, ts, "DELETE", "/api/v1/kb/test/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", resp.StatusCode)
	}

	// Get should 404.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_Entities(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

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

	// List entities.
	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/entities", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}
	var listResult struct {
		Entities []*domain.Entity `json:"entities"`
	}
	decodeJSON(t, resp, &listResult)
	if len(listResult.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(listResult.Entities))
	}

	// Get entity.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/entities/e1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", resp.StatusCode)
	}
	var entity domain.Entity
	decodeJSON(t, resp, &entity)
	if entity.Name != "Alice" {
		t.Errorf("expected Alice, got %q", entity.Name)
	}

	// Delete entity.
	resp = doJSON(t, ts, "DELETE", "/api/v1/kb/test/entities/e1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", resp.StatusCode)
	}

	// Should be gone.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/entities/e1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_Relations(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

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

	// List all relations.
	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/relations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}
	var listResult struct {
		Relations []*domain.Relation `json:"relations"`
	}
	decodeJSON(t, resp, &listResult)
	if len(listResult.Relations) != 1 {
		t.Errorf("expected 1 relation, got %d", len(listResult.Relations))
	}

	// Get relation.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/relations/r1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", resp.StatusCode)
	}

	// Filter by entity_id.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/relations?entity_id=e1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filter: expected 200, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &listResult)
	if len(listResult.Relations) != 1 {
		t.Errorf("expected 1 relation for e1, got %d", len(listResult.Relations))
	}
}

func TestHTTP_Search(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

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
	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/search?q=Alice+engineer&mode=bm25", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: expected 200, got %d", resp.StatusCode)
	}

	var searchResult struct {
		Results []*domain.SearchResult `json:"results"`
	}
	decodeJSON(t, resp, &searchResult)
	// BM25 should find at least Alice.
	if len(searchResult.Results) == 0 {
		t.Error("expected at least one search result")
	}

	// Missing query parameter.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/search", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing q, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_Stats(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	// Global stats.
	resp := doJSON(t, ts, "GET", "/api/v1/stats", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("global stats: expected 200, got %d", resp.StatusCode)
	}

	var stats domain.MemoryStats
	decodeJSON(t, resp, &stats)

	// KB stats.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/stats", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("kb stats: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_Communities(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/communities", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("communities: expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Communities []*domain.Community `json:"communities"`
	}
	decodeJSON(t, resp, &result)
	// Empty list is fine — just verify the endpoint works.
}

func TestHTTP_ValidationErrors(t *testing.T) {
	ts, _ := setupHTTPServer(t)

	// Missing ID.
	resp := doJSON(t, ts, "POST", "/api/v1/kb/", map[string]string{"name": "no-id"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid search mode.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/search?q=hello&mode=invalid", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid mode, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid JSON body.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/kb/", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_Lifecycle(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})

	// Decay with no items should succeed.
	resp := doJSON(t, ts, "POST", "/api/v1/kb/test/lifecycle/decay", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decay: expected 200, got %d", resp.StatusCode)
	}
	var decayResult map[string]any
	decodeJSON(t, resp, &decayResult)
	if decayResult["updated"] != float64(0) {
		t.Errorf("expected 0 updated, got %v", decayResult["updated"])
	}

	// Prune with no items should succeed.
	resp = doJSON(t, ts, "POST", "/api/v1/kb/test/lifecycle/prune", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prune: expected 200, got %d", resp.StatusCode)
	}
	var pruneResult map[string]any
	decodeJSON(t, resp, &pruneResult)
	if pruneResult["pruned"] != float64(0) {
		t.Errorf("expected 0 pruned, got %v", pruneResult["pruned"])
	}
}

func TestHTTP_Episodes(t *testing.T) {
	ts, store := setupHTTPServer(t)
	ctx := context.Background()

	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test"},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})
	store.CreateEpisode(ctx, &domain.Episode{
		ID: "ep1", KBID: "test", Content: "Hello world",
		Source: "test", CreatedAt: time.Now().UTC(),
	})

	// List episodes.
	resp := doJSON(t, ts, "GET", "/api/v1/kb/test/episodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}
	var listResult struct {
		Episodes []*domain.Episode `json:"episodes"`
	}
	decodeJSON(t, resp, &listResult)
	if len(listResult.Episodes) != 1 {
		t.Errorf("expected 1 episode, got %d", len(listResult.Episodes))
	}

	// Get episode.
	resp = doJSON(t, ts, "GET", "/api/v1/kb/test/episodes/ep1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", resp.StatusCode)
	}
	var ep domain.Episode
	decodeJSON(t, resp, &ep)
	if ep.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", ep.Content)
	}
}
