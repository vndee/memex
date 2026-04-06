package graph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vndee/memex/internal/domain"
)

// RelationLoader loads valid relations for graph construction.
type RelationLoader interface {
	GetValidRelations(ctx context.Context, kbID string, at time.Time) ([]*domain.Relation, error)
}

// Store manages per-KB graph instances.
type Store struct {
	mu     sync.RWMutex
	graphs map[string]*Graph
}

// NewStore creates a graph store.
func NewStore() *Store {
	return &Store{
		graphs: make(map[string]*Graph),
	}
}

// Get returns the graph for a KB, or nil if not loaded.
func (s *Store) Get(kbID string) *Graph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.graphs[kbID]
}

// Load builds the graph for a KB from its current valid relations.
func (s *Store) Load(ctx context.Context, kbID string, loader RelationLoader) error {
	rels, err := loader.GetValidRelations(ctx, kbID, time.Now())
	if err != nil {
		return fmt.Errorf("load relations for kb %s: %w", kbID, err)
	}

	// Build the graph without per-edge locking since it's not yet published.
	g := &Graph{
		forward: make(map[string][]Edge, len(rels)),
		reverse: make(map[string][]Edge, len(rels)),
		relIdx:  make(map[string]relRef, len(rels)),
	}
	for _, r := range rels {
		g.addEdge(r.SourceID, r.TargetID, r.ID, r.Type, r.Weight)
	}

	s.mu.Lock()
	s.graphs[kbID] = g
	s.mu.Unlock()
	return nil
}

// Drop removes a KB's graph from the store.
func (s *Store) Drop(kbID string) {
	s.mu.Lock()
	delete(s.graphs, kbID)
	s.mu.Unlock()
}

// Has returns whether a graph is loaded for the given KB.
func (s *Store) Has(kbID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.graphs[kbID]
	return ok
}
