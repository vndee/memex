package search

import (
	"math"
	"testing"

	"github.com/vndee/memex/internal/domain"
)

func TestFuseRRF_SingleList(t *testing.T) {
	lists := []rankedList{{
		name: "bm25",
		results: []*domain.SearchResult{
			{ID: "a", Score: 10},
			{ID: "b", Score: 5},
		},
	}}

	fused := fuseRRF(lists, 60, 10)

	if len(fused) != 2 {
		t.Fatalf("want 2 results, got %d", len(fused))
	}
	if fused[0].ID != "a" {
		t.Errorf("want first result 'a', got %q", fused[0].ID)
	}
	// RRF score for rank 1: 1/(60+1) ≈ 0.01639
	expected := 1.0 / 61.0
	if math.Abs(fused[0].Score-expected) > 1e-9 {
		t.Errorf("want score %.6f, got %.6f", expected, fused[0].Score)
	}
}

func TestFuseRRF_OverlappingLists(t *testing.T) {
	lists := []rankedList{
		{
			name: "bm25",
			results: []*domain.SearchResult{
				{ID: "a", Type: "entity", Content: "Alice"},
				{ID: "b", Type: "entity", Content: "Bob"},
			},
		},
		{
			name: "vector",
			results: []*domain.SearchResult{
				{ID: "b", Type: "entity", Content: "Bob"},
				{ID: "c", Type: "entity", Content: "Charlie"},
			},
		},
	}

	fused := fuseRRF(lists, 60, 10)

	// b appears in both lists at rank 1 (bm25) and rank 0 (vector)
	// b_score = 1/(60+2) + 1/(60+1) = 1/62 + 1/61
	// a_score = 1/(60+1) = 1/61
	// c_score = 1/(60+2) = 1/62
	// Order: b > a > c

	if fused[0].ID != "b" {
		t.Errorf("want b first (appears in both lists), got %q", fused[0].ID)
	}
	if fused[1].ID != "a" {
		t.Errorf("want a second, got %q", fused[1].ID)
	}
	if fused[2].ID != "c" {
		t.Errorf("want c third, got %q", fused[2].ID)
	}
}

func TestFuseRRF_TopK(t *testing.T) {
	results := make([]*domain.SearchResult, 20)
	for i := range results {
		results[i] = &domain.SearchResult{ID: string(rune('a' + i))}
	}

	lists := []rankedList{{name: "bm25", results: results}}
	fused := fuseRRF(lists, 60, 5)

	if len(fused) != 5 {
		t.Errorf("want 5 results, got %d", len(fused))
	}
}

func TestFuseRRF_EmptyLists(t *testing.T) {
	fused := fuseRRF(nil, 60, 10)
	if len(fused) != 0 {
		t.Errorf("want 0 results, got %d", len(fused))
	}

	fused = fuseRRF([]rankedList{{name: "bm25", results: nil}}, 60, 10)
	if len(fused) != 0 {
		t.Errorf("want 0 results, got %d", len(fused))
	}
}

func TestFuseRRF_DoesNotMutateInput(t *testing.T) {
	r := &domain.SearchResult{ID: "a", Score: 99.0}
	lists := []rankedList{{name: "bm25", results: []*domain.SearchResult{r}}}

	fuseRRF(lists, 60, 10)

	if r.Score != 99.0 {
		t.Errorf("input was mutated: score changed to %.6f", r.Score)
	}
}
