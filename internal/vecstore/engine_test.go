package vecstore

import "testing"

func TestEngine_BasicFlow(t *testing.T) {
	e := NewEngine(EngineConfig{HNSWThreshold: 100})

	e.EnsureIndex("kb1", 4)

	if err := e.Add("kb1", "a", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := e.Add("kb1", "b", []float32{0, 1, 0, 0}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if e.Len("kb1") != 2 {
		t.Errorf("Len: want 2, got %d", e.Len("kb1"))
	}

	results, err := e.Search("kb1", []float32{1, 0, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "a" {
		t.Errorf("want [{a, ...}], got %v", results)
	}

	if err := e.Remove("kb1", "a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if e.Len("kb1") != 1 {
		t.Errorf("after remove: want 1, got %d", e.Len("kb1"))
	}
}

func TestEngine_NoIndex(t *testing.T) {
	e := NewEngine(EngineConfig{})
	_, err := e.Search("nonexistent", []float32{1, 0, 0}, 1)
	if err == nil {
		t.Error("expected error for non-existent KB index")
	}
}

func TestEngine_PerKBIsolation(t *testing.T) {
	e := NewEngine(EngineConfig{})

	e.EnsureIndex("kb1", 3)
	e.EnsureIndex("kb2", 3)

	e.Add("kb1", "a", []float32{1, 0, 0})
	e.Add("kb2", "b", []float32{0, 1, 0})

	if e.Len("kb1") != 1 || e.Len("kb2") != 1 {
		t.Errorf("per-KB isolation failed")
	}

	results, _ := e.Search("kb1", []float32{0, 1, 0}, 10)
	for _, hit := range results {
		if hit.ID == "b" {
			t.Error("kb1 search returned kb2's vector")
		}
	}
}

func TestEngine_UpgradeToHNSW(t *testing.T) {
	threshold := 50
	e := NewEngine(EngineConfig{HNSWThreshold: threshold})
	e.EnsureIndex("kb1", 16)

	for i := range threshold + 10 {
		v := randomVector(16)
		e.Add("kb1", "v"+string(rune('A'+i%26))+string(rune('0'+i/26)), v)
	}

	// Should have upgraded to HNSW.
	e.mu.RLock()
	idx := e.indexes["kb1"]
	e.mu.RUnlock()
	if !idx.isHNSW() {
		t.Error("expected HNSW upgrade after exceeding threshold")
	}

	// Search should still work.
	query := randomVector(16)
	results, err := e.Search("kb1", query, 5)
	if err != nil {
		t.Fatalf("Search after upgrade: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("want 5 results, got %d", len(results))
	}
}

func TestEngine_LoadIndex(t *testing.T) {
	e := NewEngine(EngineConfig{})

	vecs := map[string][]float32{
		"a": {1, 0, 0},
		"b": {0, 1, 0},
		"c": {0, 0, 1},
	}
	e.LoadIndex("kb1", 3, vecs)

	if e.Len("kb1") != 3 {
		t.Errorf("LoadIndex: want 3, got %d", e.Len("kb1"))
	}

	results, _ := e.Search("kb1", []float32{1, 0, 0}, 1)
	if results[0].ID != "a" {
		t.Errorf("want 'a', got %q", results[0].ID)
	}
}

func TestEngine_DropIndex(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.EnsureIndex("kb1", 3)
	e.Add("kb1", "a", []float32{1, 0, 0})

	e.DropIndex("kb1")
	if e.HasIndex("kb1") {
		t.Error("index should be dropped")
	}
}

func TestEngine_DimensionMismatch(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.EnsureIndex("kb1", 3)

	err := e.Add("kb1", "a", []float32{1, 0}) // wrong dim
	if err == nil {
		t.Error("expected dimension mismatch error from Add")
	}

	e.Add("kb1", "a", []float32{1, 0, 0}) // correct dim

	_, err = e.Search("kb1", []float32{1, 0}, 1) // wrong dim
	if err == nil {
		t.Error("expected dimension mismatch error from Search")
	}
}
