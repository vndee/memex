package graph

import "testing"

func TestNeighbors_SingleHop(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("a", "c", "r2", "works_on", 0.8)

	dist := g.Neighbors([]string{"a"}, 1)

	if dist["a"] != 0 {
		t.Errorf("seed distance: want 0, got %d", dist["a"])
	}
	if dist["b"] != 1 {
		t.Errorf("b distance: want 1, got %d", dist["b"])
	}
	if dist["c"] != 1 {
		t.Errorf("c distance: want 1, got %d", dist["c"])
	}
	if len(dist) != 3 {
		t.Errorf("want 3 nodes, got %d", len(dist))
	}
}

func TestNeighbors_TwoHops(t *testing.T) {
	// a -> b -> c -> d
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("b", "c", "r2", "knows", 1.0)
	g.AddEdge("c", "d", "r3", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 2)

	if dist["a"] != 0 || dist["b"] != 1 || dist["c"] != 2 {
		t.Errorf("unexpected distances: %v", dist)
	}
	if _, ok := dist["d"]; ok {
		t.Error("d should not be reachable in 2 hops from a")
	}
}

func TestNeighbors_ReverseTraversal(t *testing.T) {
	// a -> b (directed), but BFS traverses both directions
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)

	// Starting from b should reach a via reverse edge.
	dist := g.Neighbors([]string{"b"}, 1)

	if dist["a"] != 1 {
		t.Errorf("reverse traversal: want a at distance 1, got %d", dist["a"])
	}
}

func TestNeighbors_MultipleSeeds(t *testing.T) {
	//   a - b
	//   c - d
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("c", "d", "r2", "knows", 1.0)

	dist := g.Neighbors([]string{"a", "c"}, 1)

	if len(dist) != 4 {
		t.Errorf("want 4 nodes, got %d: %v", len(dist), dist)
	}
}

func TestNeighbors_DisconnectedComponents(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("x", "y", "r2", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 10)

	if _, ok := dist["x"]; ok {
		t.Error("disconnected component should not be reachable")
	}
	if _, ok := dist["y"]; ok {
		t.Error("disconnected component should not be reachable")
	}
}

func TestNeighbors_ZeroHops(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 0)

	if len(dist) != 1 || dist["a"] != 0 {
		t.Errorf("zero hops: want only seed, got %v", dist)
	}
}

func TestNeighbors_EmptyGraph(t *testing.T) {
	g := New()
	dist := g.Neighbors([]string{"a"}, 2)

	if len(dist) != 1 || dist["a"] != 0 {
		t.Errorf("empty graph: want seed only, got %v", dist)
	}
}

func TestAddEdge_Dedup(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("a", "b", "r1", "knows", 1.0) // duplicate relID

	if g.EdgeCount() != 1 {
		t.Errorf("dedup: want 1 edge, got %d", g.EdgeCount())
	}
}

func TestRemoveEdge(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("a", "c", "r2", "uses", 0.5)

	g.RemoveEdge("r1")

	if g.EdgeCount() != 1 {
		t.Errorf("after remove: want 1 edge, got %d", g.EdgeCount())
	}

	dist := g.Neighbors([]string{"a"}, 1)
	if _, ok := dist["b"]; ok {
		t.Error("b should not be reachable after edge removal")
	}
	if dist["c"] != 1 {
		t.Error("c should still be reachable")
	}
}

func TestEntityCount(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)
	g.AddEdge("b", "c", "r2", "uses", 1.0)

	if g.EntityCount() != 3 {
		t.Errorf("want 3 entities, got %d", g.EntityCount())
	}
}

func TestHasEntity(t *testing.T) {
	g := New()
	g.AddEdge("a", "b", "r1", "knows", 1.0)

	if !g.HasEntity("a") {
		t.Error("a should exist")
	}
	if !g.HasEntity("b") {
		t.Error("b should exist")
	}
	if g.HasEntity("z") {
		t.Error("z should not exist")
	}
}
