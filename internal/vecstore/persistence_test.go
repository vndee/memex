package vecstore

import (
	"math"
	"testing"
)

func TestHNSW_MarshalUnmarshal(t *testing.T) {
	h := NewHNSW(4, 4, 32)
	h.Add("a", []float32{1, 0, 0, 0})
	h.Add("b", []float32{0, 1, 0, 0})
	h.Add("c", []float32{0.5, 0.5, 0, 0})

	data := MarshalHNSW(h)
	if len(data) == 0 {
		t.Fatal("MarshalHNSW produced empty output")
	}

	h2, err := UnmarshalHNSW(data)
	if err != nil {
		t.Fatalf("UnmarshalHNSW: %v", err)
	}

	if h2.Len() != h.Len() {
		t.Errorf("Len: want %d, got %d", h.Len(), h2.Len())
	}
	if h2.dim != h.dim {
		t.Errorf("dim: want %d, got %d", h.dim, h2.dim)
	}
	if h2.m != h.m {
		t.Errorf("m: want %d, got %d", h.m, h2.m)
	}

	// Verify search still works.
	results := h2.Search([]float32{1, 0, 0, 0}, 1, 0)
	if len(results) != 1 {
		t.Fatalf("want 1 result from restored index, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("restored index: closest should be 'a', got %q", results[0].ID)
	}

	// Verify vectors preserved.
	for id, origNode := range h.nodes {
		restoredNode, ok := h2.nodes[id]
		if !ok {
			t.Errorf("node %q missing in restored index", id)
			continue
		}
		for i, v := range origNode.vec {
			if math.Abs(float64(v-restoredNode.vec[i])) > 1e-6 {
				t.Errorf("node %q vec[%d]: want %f, got %f", id, i, v, restoredNode.vec[i])
			}
		}
	}
}

func TestUnmarshalHNSW_InvalidMagic(t *testing.T) {
	data := []byte("XXXX" + "\x01\x00\x00\x00" + "\x04\x00\x00\x00" + "\x00\x00\x00\x00")
	_, err := UnmarshalHNSW(data)
	if err == nil {
		t.Error("expected error for invalid magic bytes")
	}
}

func TestUnmarshalHNSW_TooShort(t *testing.T) {
	_, err := UnmarshalHNSW([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for short data")
	}
}
