package vecstore

import (
	"fmt"
	"testing"
)

func TestHNSW_AddSearchRemove(t *testing.T) {
	h := NewHNSW(4, 4, 32)

	h.Add("a", []float32{1, 0, 0, 0})
	h.Add("b", []float32{0.9, 0.1, 0, 0})
	h.Add("c", []float32{0, 1, 0, 0})
	h.Add("d", []float32{0, 0, 1, 0})

	if h.Len() != 4 {
		t.Fatalf("Len: want 4, got %d", h.Len())
	}

	results := h.Search([]float32{1, 0, 0, 0}, 2, 0)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("closest should be 'a', got %q (dist=%.4f)", results[0].ID, results[0].Distance)
	}

	h.Remove("a")
	if h.Len() != 3 {
		t.Fatalf("after remove: want 3, got %d", h.Len())
	}
	if h.Has("a") {
		t.Error("'a' should be removed")
	}

	results = h.Search([]float32{1, 0, 0, 0}, 1, 0)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].ID != "b" {
		t.Errorf("after removing 'a', closest should be 'b', got %q", results[0].ID)
	}
}

func TestHNSW_UpdateVector(t *testing.T) {
	h := NewHNSW(3, 4, 32)

	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0, 1, 0})

	// Update a's vector to be near b.
	h.Add("a", []float32{0.1, 0.9, 0})

	if h.Len() != 2 {
		t.Fatalf("Len after update: want 2, got %d", h.Len())
	}

	results := h.Search([]float32{0, 1, 0}, 1, 0)
	// After update, 'b' or 'a' could be closest (both near [0,1,0]).
	if results[0].ID != "b" && results[0].ID != "a" {
		t.Errorf("unexpected closest: %q", results[0].ID)
	}
}

func TestHNSW_Empty(t *testing.T) {
	h := NewHNSW(3, 4, 32)
	results := h.Search([]float32{1, 0, 0}, 5, 0)
	if len(results) != 0 {
		t.Errorf("empty index should return 0 results, got %d", len(results))
	}
}

func TestHNSW_RecallAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale test in short mode")
	}

	const (
		n   = 1000
		dim = 128
		k   = 10
	)

	// Build HNSW index.
	h := NewHNSW(dim, DefaultM, DefaultEfConstruction)
	vecs := make(map[string][]float32, n)
	for i := range n {
		id := fmt.Sprintf("v%d", i)
		v := randomVector(dim)
		vecs[id] = v
		h.Add(id, v)
	}

	// Build brute-force ground truth.
	bf := NewBruteForce(dim, false)
	for id, v := range vecs {
		bf.Add(id, v)
	}

	// Test recall over multiple queries.
	queries := 50
	totalRecall := 0.0

	for range queries {
		query := randomVector(dim)
		hnswResults := h.Search(query, k, 100)
		bfResults := bf.Search(query, k)

		bfSet := make(map[string]bool, k)
		for _, hit := range bfResults {
			bfSet[hit.ID] = true
		}

		hits := 0
		for _, hit := range hnswResults {
			if bfSet[hit.ID] {
				hits++
			}
		}
		totalRecall += float64(hits) / float64(k)
	}

	avgRecall := totalRecall / float64(queries)
	t.Logf("HNSW recall@%d over %d queries: %.1f%%", k, queries, avgRecall*100)

	if avgRecall < 0.85 {
		t.Errorf("HNSW recall too low: %.1f%% (want >= 85%%)", avgRecall*100)
	}
}

func BenchmarkHNSW_Search_1K(b *testing.B) {
	benchHNSW(b, 1000, 768)
}

func BenchmarkHNSW_Search_10K(b *testing.B) {
	benchHNSW(b, 10000, 768)
}

func benchHNSW(b *testing.B, n, dim int) {
	h := NewHNSW(dim, DefaultM, DefaultEfConstruction)
	for i := range n {
		h.Add(fmt.Sprintf("v%d", i), randomVector(dim))
	}
	query := randomVector(dim)
	b.ResetTimer()
	for range b.N {
		h.Search(query, 10, DefaultEfSearch)
	}
}
