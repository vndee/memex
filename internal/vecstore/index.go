package vecstore

// Index is the common interface for vector search indexes.
type Index interface {
	Add(id string, vec []float32)
	Remove(id string)
	Search(query []float32, k int) []SearchHit
	Len() int
	Dim() int
	Has(id string) bool
	Get(id string) []float32
}

// hnswSearchAdapter wraps HNSW to satisfy the Index interface (which has no efSearch param).
type hnswSearchAdapter struct {
	*HNSW
	efSearch int
}

func (a *hnswSearchAdapter) Search(query []float32, k int) []SearchHit {
	return a.HNSW.Search(query, k, a.efSearch)
}
