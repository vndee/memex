package lifecycle

import (
	"context"
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
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedKB(t *testing.T, store *storage.SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	store.CreateKB(ctx, &domain.KnowledgeBase{
		ID: "test", Name: "Test",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: 4},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	})
}

func TestManager_DecayOnce(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create entity and log access (creates decay_state with strength=1.0).
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.LogAccess(ctx, "test", domain.ItemEntity, "e1")

	// Manually backdate the last_access to simulate time passing.
	store.DB().ExecContext(ctx,
		`UPDATE decay_state SET last_access = datetime('now', '-168 hours') WHERE entity_id = 'e1'`)

	mgr := NewManager(store, ManagerConfig{DecayHalfLife: 168})
	updated, err := mgr.DecayKB(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}

	if updated != 1 {
		t.Errorf("expected 1 updated, got %d", updated)
	}

	// Verify strength was reduced.
	ds, err := store.GetDecayState(ctx, "test", domain.ItemEntity, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Strength >= 1.0 {
		t.Errorf("expected strength < 1.0 after decay, got %f", ds.Strength)
	}
	// After 1 half-life, strength should be ~0.5.
	if ds.Strength < 0.3 || ds.Strength > 0.7 {
		t.Errorf("expected strength ~0.5 after 1 half-life, got %f", ds.Strength)
	}
}

func TestManager_PruneOnce(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create entity with very low strength.
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.LogAccess(ctx, "test", domain.ItemEntity, "e1")
	store.UpdateDecayState(ctx, &domain.DecayState{
		EntityType: domain.ItemEntity, EntityID: "e1", KBID: "test",
		Strength: 0.01, AccessCount: 1, LastAccess: time.Now().UTC(),
	})

	mgr := NewManager(store, ManagerConfig{PruneThreshold: 0.05})
	pruned, err := mgr.PruneKB(ctx, "test", 0)
	if err != nil {
		t.Fatal(err)
	}

	if pruned != 1 {
		t.Errorf("expected 1 pruned, got %d", pruned)
	}

	// Entity should be gone.
	_, err = store.GetEntity(ctx, "test", "e1")
	if err == nil {
		t.Error("expected entity to be deleted")
	}
}

func TestManager_PruneProtectsConnectedEntities(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create two entities connected by a relation.
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

	// Set both entities to low strength.
	store.LogAccess(ctx, "test", domain.ItemEntity, "e1")
	store.LogAccess(ctx, "test", domain.ItemEntity, "e2")
	store.UpdateDecayState(ctx, &domain.DecayState{
		EntityType: domain.ItemEntity, EntityID: "e1", KBID: "test",
		Strength: 0.01, AccessCount: 1, LastAccess: time.Now().UTC(),
	})
	store.UpdateDecayState(ctx, &domain.DecayState{
		EntityType: domain.ItemEntity, EntityID: "e2", KBID: "test",
		Strength: 0.01, AccessCount: 1, LastAccess: time.Now().UTC(),
	})

	mgr := NewManager(store, ManagerConfig{PruneThreshold: 0.05})
	pruned, err := mgr.PruneKB(ctx, "test", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Both entities have relations, so neither should be pruned.
	if pruned != 0 {
		t.Errorf("expected 0 pruned (entities have relations), got %d", pruned)
	}

	// Both entities should still exist.
	_, err = store.GetEntity(ctx, "test", "e1")
	if err != nil {
		t.Error("e1 should not be pruned (has relation)")
	}
	_, err = store.GetEntity(ctx, "test", "e2")
	if err != nil {
		t.Error("e2 should not be pruned (has relation)")
	}
}

func TestConsolidator_FindAndMerge(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create two entities with very similar embeddings.
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice Smith", Type: "person",
		Summary: "Software engineer at Acme", Embedding: []float32{1, 0, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Alice S.", Type: "person",
		Summary: "Engineer", Embedding: []float32{0.99, 0.01, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Add a relation to e1 (making it the "richer" entity).
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e3", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", Embedding: []float32{0, 1, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e3",
		Type: "knows", Summary: "Alice knows Bob", Weight: 1.0,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	// Also add a relation from e2 to verify it gets redirected.
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r2", KBID: "test", SourceID: "e2", TargetID: "e3",
		Type: "works_with", Summary: "Alice works with Bob", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	consolidator := NewConsolidator(store, 0.90) // lower threshold to catch our test pair

	// Find candidates.
	pairs, err := consolidator.FindCandidates(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) == 0 {
		t.Fatal("expected at least one candidate pair")
	}

	// Verify the pair.
	pair := pairs[0]
	if pair.Survivor.ID != "e1" {
		t.Errorf("expected e1 as survivor (has more relations), got %s", pair.Survivor.ID)
	}
	if pair.Merged.ID != "e2" {
		t.Errorf("expected e2 as merged, got %s", pair.Merged.ID)
	}
	if pair.Score < 0.90 {
		t.Errorf("expected similarity >= 0.90, got %f", pair.Score)
	}

	// Run full consolidation.
	result, err := consolidator.RunConsolidation(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Merged != 1 {
		t.Errorf("expected 1 merged, got %d", result.Merged)
	}

	// e2 should be gone.
	_, err = store.GetEntity(ctx, "test", "e2")
	if err == nil {
		t.Error("expected e2 to be deleted after merge")
	}

	// e1 should still exist.
	_, err = store.GetEntity(ctx, "test", "e1")
	if err != nil {
		t.Error("expected e1 (survivor) to still exist")
	}

	// r2 should now point to e1 instead of e2.
	rel, err := store.GetRelation(ctx, "test", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if rel.SourceID != "e1" {
		t.Errorf("expected r2 source to be redirected to e1, got %s", rel.SourceID)
	}
}

func TestConsolidator_NoCandidatesForDissimilar(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create entities with very different embeddings.
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", Embedding: []float32{1, 0, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Project X", Type: "project",
		Summary: "A large project", Embedding: []float32{0, 0, 0, 1},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	consolidator := NewConsolidator(store, 0.92)
	pairs, err := consolidator.FindCandidates(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 candidates for dissimilar entities, got %d", len(pairs))
	}
}

func TestBatchUpdateDecayStrength(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create entity and log access.
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.LogAccess(ctx, "test", domain.ItemEntity, "e1")

	// Backdate access to 1 week ago.
	store.DB().ExecContext(ctx,
		`UPDATE decay_state SET last_access = datetime('now', '-168 hours') WHERE entity_id = 'e1'`)

	// Run batch update directly.
	n, err := store.BatchUpdateDecayStrength(ctx, "test", 168)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 updated, got %d", n)
	}

	ds, err := store.GetDecayState(ctx, "test", domain.ItemEntity, "e1")
	if err != nil {
		t.Fatal(err)
	}
	// After 1 half-life, strength ~0.5.
	if ds.Strength < 0.3 || ds.Strength > 0.7 {
		t.Errorf("expected ~0.5 after 1 half-life, got %f", ds.Strength)
	}
}

func TestRedirectRelations(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "a", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Alice2", Type: "person",
		Summary: "b", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e3", KBID: "test", Name: "Bob", Type: "person",
		Summary: "c", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e2", TargetID: "e3",
		Type: "knows", Summary: "x", Weight: 1.0,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	// Redirect e2 -> e1.
	n, err := store.RedirectRelations(ctx, "test", "e2", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 redirected, got %d", n)
	}

	rel, err := store.GetRelation(ctx, "test", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if rel.SourceID != "e1" {
		t.Errorf("expected source_id=e1, got %s", rel.SourceID)
	}
}
