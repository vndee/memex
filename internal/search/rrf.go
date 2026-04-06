package search

import (
	"sort"

	"github.com/vndee/memex/internal/domain"
)

// rankedList is a named list of search results in ranked order (best first).
type rankedList struct {
	name    string // "bm25", "vector", "graph"
	results []*domain.SearchResult
}

// fuseRRF merges multiple ranked lists using Reciprocal Rank Fusion.
// k is the RRF constant (typically 60). Returns fused results sorted by
// combined RRF score descending, limited to topK.
//
// Reference: Cormack, Clarke & Buettcher, "Reciprocal Rank Fusion outperforms
// Condorcet and individual Rank Learning Methods", SIGIR 2009.
func fuseRRF(lists []rankedList, k float64, topK int) []*domain.SearchResult {
	type entry struct {
		result *domain.SearchResult
		score  float64
	}

	fused := make(map[string]*entry) // keyed by result ID

	for _, list := range lists {
		for rank, r := range list.results {
			e, ok := fused[r.ID]
			if !ok {
				// Clone the result so we don't mutate the original.
				clone := *r
				e = &entry{result: &clone}
				fused[r.ID] = e
			}
			e.score += 1.0 / (k + float64(rank+1))
		}
	}

	sorted := make([]*domain.SearchResult, 0, len(fused))
	for _, e := range fused {
		e.result.Score = e.score
		sorted = append(sorted, e.result)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	if len(sorted) > topK {
		sorted = sorted[:topK]
	}
	return sorted
}
