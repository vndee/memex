package graph

import "sync"

// Edge represents a directed edge in the knowledge graph.
type Edge struct {
	TargetID string
	RelID    string  // relation ID for metadata lookup
	Type     string  // relation type ("works_on", "knows", etc.)
	Weight   float64 // confidence weight from extraction
}

// Graph is a per-KB in-memory directed graph using adjacency lists.
// Supports BFS neighborhood expansion for hybrid search.
type Graph struct {
	forward map[string][]Edge // entityID -> outgoing edges
	reverse map[string][]Edge // entityID -> incoming edges
	relIdx  map[string]relRef // relID -> source/target for removal
	mu      sync.RWMutex
}

type relRef struct {
	sourceID, targetID string
}

// New creates an empty graph.
func New() *Graph {
	return &Graph{
		forward: make(map[string][]Edge),
		reverse: make(map[string][]Edge),
		relIdx:  make(map[string]relRef),
	}
}

// AddEdge adds a directed edge from sourceID to targetID. Thread-safe.
func (g *Graph) AddEdge(sourceID, targetID, relID, relType string, weight float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addEdge(sourceID, targetID, relID, relType, weight)
}

// addEdge is the lock-free inner implementation. Caller must hold g.mu.
func (g *Graph) addEdge(sourceID, targetID, relID, relType string, weight float64) {
	if _, exists := g.relIdx[relID]; exists {
		return
	}

	edge := Edge{
		TargetID: targetID,
		RelID:    relID,
		Type:     relType,
		Weight:   weight,
	}
	g.forward[sourceID] = append(g.forward[sourceID], edge)

	rev := Edge{
		TargetID: sourceID,
		RelID:    relID,
		Type:     relType,
		Weight:   weight,
	}
	g.reverse[targetID] = append(g.reverse[targetID], rev)

	g.relIdx[relID] = relRef{sourceID: sourceID, targetID: targetID}
}

// RemoveEdge removes an edge by relation ID.
func (g *Graph) RemoveEdge(relID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ref, ok := g.relIdx[relID]
	if !ok {
		return
	}
	delete(g.relIdx, relID)

	g.forward[ref.sourceID] = removeEdge(g.forward[ref.sourceID], relID)
	g.reverse[ref.targetID] = removeEdge(g.reverse[ref.targetID], relID)
}

// Neighbors returns all entity IDs reachable within maxHops from the seeds,
// traversing edges in both directions. Returns entityID -> minimum hop distance.
// Seeds themselves are included at distance 0.
func (g *Graph) Neighbors(seeds []string, maxHops int) map[string]int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	dist := make(map[string]int, len(seeds))
	queue := make([]string, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := dist[id]; !seen {
			dist[id] = 0
			queue = append(queue, id)
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hop := dist[cur]
		if hop >= maxHops {
			continue
		}

		// Traverse outgoing and incoming edges.
		for _, edges := range [][]Edge{g.forward[cur], g.reverse[cur]} {
			for _, e := range edges {
				if _, seen := dist[e.TargetID]; !seen {
					dist[e.TargetID] = hop + 1
					queue = append(queue, e.TargetID)
				}
			}
		}
	}

	return dist
}

// HasEntity returns whether the entity has any edges.
func (g *Graph) HasEntity(entityID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.forward[entityID]) > 0 || len(g.reverse[entityID]) > 0
}

// EntityCount returns the number of unique entities (nodes with at least one edge).
func (g *Graph) EntityCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	nodes := make(map[string]struct{})
	for id := range g.forward {
		nodes[id] = struct{}{}
	}
	for id := range g.reverse {
		nodes[id] = struct{}{}
	}
	return len(nodes)
}

// EdgeCount returns the number of edges in the graph.
func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.relIdx)
}

func removeEdge(edges []Edge, relID string) []Edge {
	for i, e := range edges {
		if e.RelID == relID {
			return append(edges[:i], edges[i+1:]...)
		}
	}
	return edges
}
