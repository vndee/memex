package storage_test

import (
	"context"
	"database/sql"
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

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal("failed to create store:", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestKB(t *testing.T, store *storage.SQLiteStore, id string) *domain.KnowledgeBase {
	t.Helper()
	kb := &domain.KnowledgeBase{
		ID:   id,
		Name: id,
		EmbedConfig: domain.EmbedConfig{
			Provider: "ollama",
			Model:    "nomic-embed-text",
			Dim:      768,
		},
		LLMConfig: domain.LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateKB(context.Background(), kb); err != nil {
		t.Fatal("failed to create kb:", err)
	}
	return kb
}

// --- Knowledge Base tests ---

func TestKBCreateAndGet(t *testing.T) {
	store := newTestStore(t)
	kb := createTestKB(t, store, "test-kb")

	got, err := store.GetKB(context.Background(), kb.ID)
	if err != nil {
		t.Fatal("get kb:", err)
	}
	if got.ID != kb.ID {
		t.Errorf("got ID %q, want %q", got.ID, kb.ID)
	}
	if got.EmbedConfig.Model != "nomic-embed-text" {
		t.Errorf("got embed model %q, want %q", got.EmbedConfig.Model, "nomic-embed-text")
	}
}

func TestKBList(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	kbs, err := store.ListKBs(context.Background())
	if err != nil {
		t.Fatal("list kbs:", err)
	}
	if len(kbs) != 2 {
		t.Fatalf("got %d kbs, want 2", len(kbs))
	}
}

func TestKBDelete(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "to-delete")

	if err := store.DeleteKB(context.Background(), "to-delete"); err != nil {
		t.Fatal("delete kb:", err)
	}

	_, err := store.GetKB(context.Background(), "to-delete")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

// --- Episode tests ---

func TestEpisodeCRUD(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	ep := &domain.Episode{
		ID:        "ep-001",
		KBID:      "kb1",
		Content:   "Met with Alice about the API redesign",
		Source:    "cli",
		Metadata:  map[string]string{"tag": "meeting"},
		CreatedAt: time.Now().UTC(),
	}

	// Create
	if err := store.CreateEpisode(context.Background(), ep); err != nil {
		t.Fatal("create episode:", err)
	}

	// Get
	got, err := store.GetEpisode(context.Background(), "kb1", "ep-001")
	if err != nil {
		t.Fatal("get episode:", err)
	}
	if got.Content != ep.Content {
		t.Errorf("got content %q, want %q", got.Content, ep.Content)
	}
	if got.Metadata["tag"] != "meeting" {
		t.Errorf("got metadata tag %q, want %q", got.Metadata["tag"], "meeting")
	}

	// List
	eps, err := store.ListEpisodes(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal("list episodes:", err)
	}
	if len(eps) != 1 {
		t.Fatalf("got %d episodes, want 1", len(eps))
	}

	// Delete
	if err := store.DeleteEpisode(context.Background(), "kb1", "ep-001"); err != nil {
		t.Fatal("delete episode:", err)
	}
	_, err = store.GetEpisode(context.Background(), "kb1", "ep-001")
	if err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

func TestEpisodeScopeEnforced(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	ep := &domain.Episode{
		ID:        "ep-001",
		KBID:      "kb1",
		Content:   "Memory in kb1",
		Source:    "cli",
		Metadata:  map[string]string{},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateEpisode(context.Background(), ep); err != nil {
		t.Fatal("create episode:", err)
	}

	if _, err := store.GetEpisode(context.Background(), "kb2", "ep-001"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows for wrong kb, got %v", err)
	}

	if err := store.DeleteEpisode(context.Background(), "kb2", "ep-001"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows deleting from wrong kb, got %v", err)
	}

	if _, err := store.GetEpisode(context.Background(), "kb1", "ep-001"); err != nil {
		t.Fatalf("episode should remain in original kb: %v", err)
	}
}

// --- Entity tests ---

func TestEntityCRUD(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	e := &domain.Entity{
		ID:        "ent-001",
		KBID:      "kb1",
		Name:      "Alice",
		Type:      "person",
		Summary:   "Software engineer working on API redesign",
		Embedding: []float32{0.1, 0.2, 0.3},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// Create
	if err := store.CreateEntity(context.Background(), e); err != nil {
		t.Fatal("create entity:", err)
	}

	// Get
	got, err := store.GetEntity(context.Background(), "kb1", "ent-001")
	if err != nil {
		t.Fatal("get entity:", err)
	}
	if got.Name != "Alice" {
		t.Errorf("got name %q, want %q", got.Name, "Alice")
	}
	if len(got.Embedding) != 3 {
		t.Fatalf("got %d embedding dims, want 3", len(got.Embedding))
	}
	if got.Embedding[0] != 0.1 {
		t.Errorf("got embedding[0] = %f, want 0.1", got.Embedding[0])
	}

	// Update
	e.Summary = "Senior engineer"
	if err := store.UpdateEntity(context.Background(), e); err != nil {
		t.Fatal("update entity:", err)
	}
	got, _ = store.GetEntity(context.Background(), "kb1", "ent-001")
	if got.Summary != "Senior engineer" {
		t.Errorf("got summary %q, want %q", got.Summary, "Senior engineer")
	}

	// FindByName
	found, err := store.FindEntitiesByName(context.Background(), "kb1", "alice")
	if err != nil {
		t.Fatal("find by name:", err)
	}
	if len(found) != 1 {
		t.Fatalf("got %d entities, want 1", len(found))
	}

	// List
	all, err := store.ListEntities(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal("list entities:", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d entities, want 1", len(all))
	}

	// Delete
	if err := store.DeleteEntity(context.Background(), "kb1", "ent-001"); err != nil {
		t.Fatal("delete entity:", err)
	}
}

func TestEntityScopeEnforced(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	now := time.Now().UTC()
	e := &domain.Entity{
		ID:        "ent-001",
		KBID:      "kb1",
		Name:      "Alice",
		Type:      "person",
		Summary:   "Original summary",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateEntity(context.Background(), e); err != nil {
		t.Fatal("create entity:", err)
	}

	if _, err := store.GetEntity(context.Background(), "kb2", "ent-001"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows for wrong kb, got %v", err)
	}

	wrongKBUpdate := *e
	wrongKBUpdate.KBID = "kb2"
	wrongKBUpdate.Summary = "Mutated from wrong kb"
	if err := store.UpdateEntity(context.Background(), &wrongKBUpdate); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows updating from wrong kb, got %v", err)
	}

	got, err := store.GetEntity(context.Background(), "kb1", "ent-001")
	if err != nil {
		t.Fatal("get entity:", err)
	}
	if got.Summary != "Original summary" {
		t.Fatalf("entity was mutated across KB boundary: got %q", got.Summary)
	}

	if err := store.DeleteEntity(context.Background(), "kb2", "ent-001"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows deleting from wrong kb, got %v", err)
	}

	if _, err := store.GetEntity(context.Background(), "kb1", "ent-001"); err != nil {
		t.Fatalf("entity should remain in original kb: %v", err)
	}
}

func TestGetEntitiesByIDs(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	now := time.Now().UTC()
	for _, e := range []*domain.Entity{
		{ID: "ent-001", KBID: "kb1", Name: "Alice", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-002", KBID: "kb1", Name: "Bob", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-003", KBID: "kb2", Name: "Charlie", Type: "person", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateEntity(context.Background(), e); err != nil {
			t.Fatal("create entity:", err)
		}
	}

	entities, err := store.GetEntitiesByIDs(context.Background(), "kb1", []string{"ent-001", "ent-002", "ent-003", "ent-missing"})
	if err != nil {
		t.Fatal("get entities by ids:", err)
	}
	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2", len(entities))
	}
	if entities["ent-001"] == nil || entities["ent-001"].Name != "Alice" {
		t.Fatal("expected ent-001 (Alice) in result")
	}
	if entities["ent-002"] == nil || entities["ent-002"].Name != "Bob" {
		t.Fatal("expected ent-002 (Bob) in result")
	}
	if _, ok := entities["ent-003"]; ok {
		t.Fatal("did not expect kb2 entity in kb1 result")
	}
}

func TestGetEntitiesByIDs_LargeBatch(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	now := time.Now().UTC()

	const n = 905
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ent-%04d", i)
		ids = append(ids, id)
		if err := store.CreateEntity(context.Background(), &domain.Entity{
			ID: id, KBID: "kb1", Name: id, Type: "entity", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal("create entity:", err)
		}
	}

	entities, err := store.GetEntitiesByIDs(context.Background(), "kb1", ids)
	if err != nil {
		t.Fatal("get entities by ids:", err)
	}
	if len(entities) != n {
		t.Fatalf("got %d entities, want %d", len(entities), n)
	}
}

// --- Relation tests ---

func TestRelationCRUD(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	// Create entities first
	now := time.Now().UTC()
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-a", KBID: "kb1", Name: "Alice", Type: "person", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-b", KBID: "kb1", Name: "API Project", Type: "project", CreatedAt: now, UpdatedAt: now,
	})

	r := &domain.Relation{
		ID:        "rel-001",
		KBID:      "kb1",
		SourceID:  "ent-a",
		TargetID:  "ent-b",
		Type:      "works_on",
		Summary:   "Alice works on the API project",
		Weight:    1.0,
		ValidAt:   now,
		CreatedAt: now,
	}

	// Create
	if err := store.CreateRelation(context.Background(), r); err != nil {
		t.Fatal("create relation:", err)
	}

	// Get
	got, err := store.GetRelation(context.Background(), "kb1", "rel-001")
	if err != nil {
		t.Fatal("get relation:", err)
	}
	if got.Type != "works_on" {
		t.Errorf("got type %q, want %q", got.Type, "works_on")
	}
	if got.InvalidAt != nil {
		t.Error("expected nil invalid_at")
	}

	// Invalidate
	invalidTime := now.Add(24 * time.Hour)
	if err := store.InvalidateRelation(context.Background(), "kb1", "rel-001", invalidTime); err != nil {
		t.Fatal("invalidate relation:", err)
	}
	got, _ = store.GetRelation(context.Background(), "kb1", "rel-001")
	if got.InvalidAt == nil {
		t.Fatal("expected non-nil invalid_at")
	}

	// GetRelationsForEntity
	rels, err := store.GetRelationsForEntity(context.Background(), "kb1", "ent-a")
	if err != nil {
		t.Fatal("get relations for entity:", err)
	}
	if len(rels) != 1 {
		t.Fatalf("got %d relations, want 1", len(rels))
	}

	// GetValidRelations (before invalidation)
	valid, err := store.GetValidRelations(context.Background(), "kb1", now)
	if err != nil {
		t.Fatal("get valid relations:", err)
	}
	if len(valid) != 1 {
		t.Fatalf("got %d valid relations at now, want 1", len(valid))
	}

	// GetValidRelations (after invalidation)
	valid, err = store.GetValidRelations(context.Background(), "kb1", invalidTime.Add(time.Hour))
	if err != nil {
		t.Fatal("get valid relations:", err)
	}
	if len(valid) != 0 {
		t.Fatalf("got %d valid relations after invalidation, want 0", len(valid))
	}
}

func TestRelationScopeAndKBBoundaryEnforced(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	now := time.Now().UTC()
	for _, entity := range []*domain.Entity{
		{ID: "ent-a", KBID: "kb1", Name: "Alice", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-b", KBID: "kb1", Name: "Project", Type: "project", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-c", KBID: "kb2", Name: "Other KB", Type: "project", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateEntity(context.Background(), entity); err != nil {
			t.Fatal("create entity:", err)
		}
	}

	r := &domain.Relation{
		ID:        "rel-001",
		KBID:      "kb1",
		SourceID:  "ent-a",
		TargetID:  "ent-b",
		Type:      "works_on",
		Summary:   "Alice works on Project",
		Weight:    1.0,
		ValidAt:   now,
		CreatedAt: now,
	}
	if err := store.CreateRelation(context.Background(), r); err != nil {
		t.Fatal("create relation:", err)
	}

	if _, err := store.GetRelation(context.Background(), "kb2", "rel-001"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows for wrong kb, got %v", err)
	}

	if err := store.InvalidateRelation(context.Background(), "kb2", "rel-001", now.Add(time.Hour)); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows invalidating from wrong kb, got %v", err)
	}

	crossKB := &domain.Relation{
		ID:        "rel-002",
		KBID:      "kb1",
		SourceID:  "ent-a",
		TargetID:  "ent-c",
		Type:      "works_on",
		Summary:   "Should fail across KBs",
		Weight:    1.0,
		ValidAt:   now,
		CreatedAt: now,
	}
	if err := store.CreateRelation(context.Background(), crossKB); err == nil {
		t.Fatal("expected cross-KB relation creation to fail")
	}
}

func TestGetRelationsByIDs(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")
	createTestKB(t, store, "kb2")

	now := time.Now().UTC()
	for _, e := range []*domain.Entity{
		{ID: "ent-a", KBID: "kb1", Name: "Alice", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-b", KBID: "kb1", Name: "Bob", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-c", KBID: "kb2", Name: "Carol", Type: "person", CreatedAt: now, UpdatedAt: now},
		{ID: "ent-d", KBID: "kb2", Name: "Dan", Type: "person", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateEntity(context.Background(), e); err != nil {
			t.Fatal("create entity:", err)
		}
	}

	for _, r := range []*domain.Relation{
		{ID: "rel-001", KBID: "kb1", SourceID: "ent-a", TargetID: "ent-b", Type: "knows", Weight: 1, ValidAt: now, CreatedAt: now},
		{ID: "rel-002", KBID: "kb2", SourceID: "ent-c", TargetID: "ent-d", Type: "knows", Weight: 1, ValidAt: now, CreatedAt: now},
	} {
		if err := store.CreateRelation(context.Background(), r); err != nil {
			t.Fatal("create relation:", err)
		}
	}

	rels, err := store.GetRelationsByIDs(context.Background(), "kb1", []string{"rel-001", "rel-002", "rel-missing"})
	if err != nil {
		t.Fatal("get relations by ids:", err)
	}
	if len(rels) != 1 {
		t.Fatalf("got %d relations, want 1", len(rels))
	}
	if rels["rel-001"] == nil || rels["rel-001"].SourceID != "ent-a" || rels["rel-001"].TargetID != "ent-b" {
		t.Fatal("expected rel-001 with correct endpoints")
	}
	if _, ok := rels["rel-002"]; ok {
		t.Fatal("did not expect kb2 relation in kb1 result")
	}
}

// --- FTS5 Search tests ---

func TestSearchFTS(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	now := time.Now().UTC()
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-1", KBID: "kb1", Name: "Golang", Type: "technology",
		Summary: "A programming language designed at Google", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-2", KBID: "kb1", Name: "Rust", Type: "technology",
		Summary: "A systems programming language focused on safety", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEpisode(context.Background(), &domain.Episode{
		ID: "ep-1", KBID: "kb1", Content: "Discussed using Golang for the new project",
		Source: "cli", Metadata: map[string]string{}, CreatedAt: now,
	})

	results, err := store.SearchFTS(context.Background(), "kb1", "Golang", 10)
	if err != nil {
		t.Fatal("search fts:", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 search result for 'Golang'")
	}

	// Check that we find both entity and episode
	types := map[string]bool{}
	for _, r := range results {
		types[r.Type] = true
	}
	if !types["entity"] {
		t.Error("expected entity in search results")
	}
	if !types["episode"] {
		t.Error("expected episode in search results")
	}
}

func TestSearchFTSHonorsLimit(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	now := time.Now().UTC()
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-1", KBID: "kb1", Name: "Golang", Type: "technology",
		Summary: "Golang entity", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-2", KBID: "kb1", Name: "Project", Type: "project",
		Summary: "Uses Golang heavily", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEpisode(context.Background(), &domain.Episode{
		ID: "ep-1", KBID: "kb1", Content: "Golang episode", Source: "cli",
		Metadata: map[string]string{}, CreatedAt: now,
	})
	store.CreateRelation(context.Background(), &domain.Relation{
		ID:        "rel-1",
		KBID:      "kb1",
		SourceID:  "ent-1",
		TargetID:  "ent-2",
		Type:      "uses",
		Summary:   "Golang relation",
		Weight:    1.0,
		ValidAt:   now,
		CreatedAt: now,
	})

	results, err := store.SearchFTS(context.Background(), "kb1", "Golang", 1)
	if err != nil {
		t.Fatal("search fts:", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// --- Access log and decay tests ---

func TestAccessLogAndDecay(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	// Log access
	if err := store.LogAccess(context.Background(), "kb1", "entity", "ent-1"); err != nil {
		t.Fatal("log access:", err)
	}

	// Get decay state
	ds, err := store.GetDecayState(context.Background(), "kb1", "entity", "ent-1")
	if err != nil {
		t.Fatal("get decay state:", err)
	}
	if ds.Strength != 1.0 {
		t.Errorf("got strength %f, want 1.0", ds.Strength)
	}
	if ds.AccessCount != 1 {
		t.Errorf("got access_count %d, want 1", ds.AccessCount)
	}

	// Log access again
	store.LogAccess(context.Background(), "kb1", "entity", "ent-1")
	ds, _ = store.GetDecayState(context.Background(), "kb1", "entity", "ent-1")
	if ds.AccessCount != 2 {
		t.Errorf("got access_count %d, want 2", ds.AccessCount)
	}

	// Update decay state
	ds.Strength = 0.5
	if err := store.UpdateDecayState(context.Background(), ds); err != nil {
		t.Fatal("update decay state:", err)
	}

	// List weak states
	weak, err := store.ListDecayStates(context.Background(), "kb1", 0.6)
	if err != nil {
		t.Fatal("list decay states:", err)
	}
	if len(weak) != 1 {
		t.Fatalf("got %d weak states, want 1", len(weak))
	}
}

// --- Stats tests ---

func TestStats(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	now := time.Now().UTC()
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-1", KBID: "kb1", Name: "Test", Type: "concept", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEpisode(context.Background(), &domain.Episode{
		ID: "ep-1", KBID: "kb1", Content: "test", Source: "cli",
		Metadata: map[string]string{}, CreatedAt: now,
	})

	// Per-KB stats
	stats, err := store.GetStats(context.Background(), "kb1")
	if err != nil {
		t.Fatal("get stats:", err)
	}
	if stats.TotalEntities != 1 {
		t.Errorf("got %d entities, want 1", stats.TotalEntities)
	}
	if stats.TotalEpisodes != 1 {
		t.Errorf("got %d episodes, want 1", stats.TotalEpisodes)
	}

	// Global stats
	global, err := store.GetStats(context.Background(), "")
	if err != nil {
		t.Fatal("get global stats:", err)
	}
	if global.TotalEntities != 1 {
		t.Errorf("got %d global entities, want 1", global.TotalEntities)
	}
}

// --- KB cascade delete tests ---

func TestKBDeleteCascade(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	now := time.Now().UTC()
	store.CreateEntity(context.Background(), &domain.Entity{
		ID: "ent-1", KBID: "kb1", Name: "Test", Type: "concept", CreatedAt: now, UpdatedAt: now,
	})
	store.CreateEpisode(context.Background(), &domain.Episode{
		ID: "ep-1", KBID: "kb1", Content: "test", Source: "cli",
		Metadata: map[string]string{}, CreatedAt: now,
	})
	if err := store.LogAccess(context.Background(), "kb1", "entity", "ent-1"); err != nil {
		t.Fatal("log access:", err)
	}

	// Delete KB should cascade
	if err := store.DeleteKB(context.Background(), "kb1"); err != nil {
		t.Fatal("delete kb:", err)
	}

	stats, _ := store.GetStats(context.Background(), "kb1")
	if stats.TotalEntities != 0 {
		t.Errorf("got %d entities after cascade, want 0", stats.TotalEntities)
	}
	if stats.TotalEpisodes != 0 {
		t.Errorf("got %d episodes after cascade, want 0", stats.TotalEpisodes)
	}

	var accessCount int
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM memory_access_log WHERE kb_id = ?`, "kb1").Scan(&accessCount); err != nil {
		t.Fatal("count access log:", err)
	}
	if accessCount != 0 {
		t.Fatalf("got %d access log rows after cascade, want 0", accessCount)
	}

	var decayCount int
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM decay_state WHERE kb_id = ?`, "kb1").Scan(&decayCount); err != nil {
		t.Fatal("count decay state:", err)
	}
	if decayCount != 0 {
		t.Fatalf("got %d decay rows after cascade, want 0", decayCount)
	}
}

func TestKBCreateValidatesProviders(t *testing.T) {
	store := newTestStore(t)

	kb := &domain.KnowledgeBase{
		ID:   "bad-kb",
		Name: "bad-kb",
		EmbedConfig: domain.EmbedConfig{
			Provider: "not-a-provider",
			Model:    "x",
		},
		LLMConfig: domain.LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := store.CreateKB(context.Background(), kb); err == nil {
		t.Fatal("expected invalid provider config to fail")
	}
}

func TestIngestionTaskQueue(t *testing.T) {
	store := newTestStore(t)
	createTestKB(t, store, "kb1")

	now := time.Now().UTC()
	if err := store.CreateEpisode(context.Background(), &domain.Episode{
		ID:        "ep-1",
		KBID:      "kb1",
		Content:   "degraded ingest",
		Source:    "cli",
		Metadata:  map[string]string{},
		CreatedAt: now,
	}); err != nil {
		t.Fatal("create episode:", err)
	}

	job := &domain.IngestionJob{
		ID:          "job-1",
		KBID:        "kb1",
		Status:      domain.JobStatusQueued,
		Content:     "test content",
		Source:      "cli",
		MaxAttempts: 3,
		CreatedAt:   now,
	}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal("create job:", err)
	}

	jobs, err := store.ListJobs(context.Background(), "kb1", domain.JobStatusQueued, 10)
	if err != nil {
		t.Fatal("list jobs:", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	if jobs[0].ID != "job-1" {
		t.Fatalf("got id %q, want %q", jobs[0].ID, "job-1")
	}
	if jobs[0].Status != domain.JobStatusQueued {
		t.Fatalf("got status %q, want %q", jobs[0].Status, domain.JobStatusQueued)
	}

	// Update to running
	if err := store.UpdateJobStatus(context.Background(), "job-1", domain.JobStatusRunning, storage.JobUpdate{}); err != nil {
		t.Fatal("update job status:", err)
	}
	got, err := store.GetJob(context.Background(), "job-1")
	if err != nil {
		t.Fatal("get job:", err)
	}
	if got.Status != domain.JobStatusRunning {
		t.Fatalf("got status %q, want %q", got.Status, domain.JobStatusRunning)
	}
	if got.StartedAt == nil {
		t.Fatal("started_at should be set")
	}
}
