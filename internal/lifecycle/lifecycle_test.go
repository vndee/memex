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

func TestUpsertRelation_CreatesNew(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	created, err := store.UpsertRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "Alice knows Bob", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true for new relation")
	}

	rel, err := store.GetRelation(ctx, "test", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Weight != 0.5 {
		t.Errorf("expected weight 0.5, got %f", rel.Weight)
	}
}

func TestUpsertRelation_StrengthensExisting(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// First insert.
	if _, err := store.UpsertRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "Alice knows Bob", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed upsert failed: %v", err)
	}

	// Second upsert with same (source, target, type) — should strengthen.
	created, err := store.UpsertRelation(ctx, &domain.Relation{
		ID: "r2", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "short", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created=false for existing relation")
	}

	// Weight should be CombineWeights(0.5, 0.5) = 0.75.
	rel, err := store.GetRelation(ctx, "test", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Weight < 0.74 || rel.Weight > 0.76 {
		t.Errorf("expected weight ~0.75 (probability union), got %f", rel.Weight)
	}

	// Summary should stay "Alice knows Bob" (longer than "short").
	if rel.Summary != "Alice knows Bob" {
		t.Errorf("expected longer summary kept, got %q", rel.Summary)
	}

	// r2 should not exist as a separate row.
	_, err = store.GetRelation(ctx, "test", "r2")
	if err == nil {
		t.Error("expected r2 not to exist (should have strengthened r1 instead)")
	}
}

func TestUpsertRelation_KeepsLongerSummary(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// First insert with short summary.
	if _, err := store.UpsertRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "knows", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed upsert failed: %v", err)
	}

	// Second upsert with longer summary — should replace.
	if _, err := store.UpsertRelation(ctx, &domain.Relation{
		ID: "r2", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "Alice has known Bob since college, they studied CS together", Weight: 0.3,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	rel, err := store.GetRelation(ctx, "test", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Summary != "Alice has known Bob since college, they studied CS together" {
		t.Errorf("expected longer summary to replace shorter one, got %q", rel.Summary)
	}
}

func TestDeduplicateRelationsAfterConsolidation(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	// Create three entities: e1 (Alice Smith), e2 (Alice S.), e3 (Bob).
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
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e3", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", Embedding: []float32{0, 1, 0, 0},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Both Alice entities "know" Bob — these should merge after consolidation.
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e3",
		Type: "knows", Summary: "Alice knows Bob from work", Weight: 0.8,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r2", KBID: "test", SourceID: "e2", TargetID: "e3",
		Type: "knows", Summary: "Alice knows Bob", Weight: 0.5,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	consolidator := NewConsolidator(store, 0.90)
	result, err := consolidator.RunConsolidation(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Merged != 1 {
		t.Fatalf("expected 1 merge, got %d", result.Merged)
	}
	if result.RelationsDeduped == 0 {
		t.Error("expected relations to be deduped after consolidation redirect")
	}

	// Should have exactly one "knows" relation from e1 to e3.
	rels, err := store.GetRelationsForEntity(ctx, "test", "e1")
	if err != nil {
		t.Fatal(err)
	}
	knowsCount := 0
	for _, r := range rels {
		if r.Type == "knows" && r.TargetID == "e3" {
			knowsCount++
			// Weight should be combined: CombineWeights(0.8, 0.5) = 0.90.
			if r.Weight < 0.89 || r.Weight > 0.91 {
				t.Errorf("expected combined weight ~0.90, got %f", r.Weight)
			}
			// Should keep longer summary.
			if r.Summary != "Alice knows Bob from work" {
				t.Errorf("expected longer summary kept, got %q", r.Summary)
			}
		}
	}
	if knowsCount != 1 {
		t.Errorf("expected exactly 1 'knows' relation after dedup, got %d", knowsCount)
	}
}

func TestDeduplicateRelationsForKB(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	store.CreateEntity(ctx, &domain.Entity{
		ID: "e2", KBID: "test", Name: "Bob", Type: "person",
		Summary: "Manager", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Create intentional duplicates.
	now := time.Now().UTC()
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "Alice knows Bob", Weight: 0.6,
		ValidAt: now, CreatedAt: now,
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r2", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "short", Weight: 0.4,
		ValidAt: now.Add(time.Hour), CreatedAt: now.Add(time.Hour),
	})
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r3", KBID: "test", SourceID: "e1", TargetID: "e2",
		Type: "knows", Summary: "x", Weight: 0.3,
		ValidAt: now.Add(2 * time.Hour), CreatedAt: now.Add(2 * time.Hour),
	})

	deleted, err := store.DeduplicateRelationsForKB(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Only the survivor should remain.
	rels, err := store.GetRelationsForEntity(ctx, "test", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation remaining, got %d", len(rels))
	}

	// Survivor should have combined weight and longest summary.
	r := rels[0]
	if r.Summary != "Alice knows Bob" {
		t.Errorf("expected longest summary, got %q", r.Summary)
	}
	// CombineWeightsMulti(0.6, 0.4, 0.3) = 1 - (0.4 * 0.6 * 0.7) = 1 - 0.168 = 0.832
	if r.Weight < 0.82 || r.Weight > 0.84 {
		t.Errorf("expected combined weight ~0.832, got %f", r.Weight)
	}
}

func TestDeduplicateRelations_SelfLoopRemoval(t *testing.T) {
	store := newTestStore(t)
	seedKB(t, store)
	ctx := context.Background()

	store.CreateEntity(ctx, &domain.Entity{
		ID: "e1", KBID: "test", Name: "Alice", Type: "person",
		Summary: "Engineer", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Create a self-loop (can happen after redirect when merged entity had relation to survivor).
	store.CreateRelation(ctx, &domain.Relation{
		ID: "r1", KBID: "test", SourceID: "e1", TargetID: "e1",
		Type: "knows", Summary: "self", Weight: 1.0,
		ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})

	deleted, err := store.DeduplicateRelationsForKB(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 self-loop deleted, got %d", deleted)
	}

	rels, err := store.GetRelationsForEntity(ctx, "test", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relations after self-loop removal, got %d", len(rels))
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
