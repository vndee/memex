package vecstore

import (
	"fmt"
	"testing"
)

func TestBruteForce_AddSearchRemove(t *testing.T) {
	bf := NewBruteForce(4, false)

	bf.Add("a", []float32{1, 0, 0, 0})
	bf.Add("b", []float32{0.9, 0.1, 0, 0})
	bf.Add("c", []float32{0, 1, 0, 0})

	if bf.Len() != 3 {
		t.Fatalf("Len: want 3, got %d", bf.Len())
	}

	results := bf.Search([]float32{1, 0, 0, 0}, 2)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("closest should be 'a', got %q", results[0].ID)
	}
	if results[1].ID != "b" {
		t.Errorf("second closest should be 'b', got %q", results[1].ID)
	}

	bf.Remove("a")
	if bf.Len() != 2 {
		t.Fatalf("after remove: want 2, got %d", bf.Len())
	}
	if bf.Has("a") {
		t.Error("'a' should be removed")
	}
}

func TestBruteForce_Quantized(t *testing.T) {
	bf := NewBruteForce(4, true)

	bf.Add("a", []float32{1, 0, 0, 0})
	bf.Add("b", []float32{0.9, 0.1, 0, 0})
	bf.Add("c", []float32{0, 1, 0, 0})

	results := bf.Search([]float32{1, 0, 0, 0}, 2)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// "a" should be closest even with quantization.
	if results[0].ID != "a" {
		t.Errorf("quantized: closest should be 'a', got %q", results[0].ID)
	}
}

func TestBruteForce_KLargerThanN(t *testing.T) {
	bf := NewBruteForce(3, false)
	bf.Add("x", []float32{1, 0, 0})

	results := bf.Search([]float32{1, 0, 0}, 10)
	if len(results) != 1 {
		t.Errorf("want 1 result (only 1 vector), got %d", len(results))
	}
}

func TestBruteForce_Empty(t *testing.T) {
	bf := NewBruteForce(3, false)
	results := bf.Search([]float32{1, 0, 0}, 5)
	if len(results) != 0 {
		t.Errorf("empty index should return 0 results, got %d", len(results))
	}
}

func BenchmarkBruteForce_Search_1K(b *testing.B) {
	benchBruteForce(b, 1000, 768, false)
}

func BenchmarkBruteForce_Search_1K_Quantized(b *testing.B) {
	benchBruteForce(b, 1000, 768, true)
}

func BenchmarkBruteForce_Search_10K(b *testing.B) {
	benchBruteForce(b, 10000, 768, false)
}

func BenchmarkBruteForce_Search_10K_Quantized(b *testing.B) {
	benchBruteForce(b, 10000, 768, true)
}

func benchBruteForce(b *testing.B, n, dim int, quantize bool) {
	bf := NewBruteForce(dim, quantize)
	for i := range n {
		bf.Add(fmt.Sprintf("v%d", i), randomVector(dim))
	}
	query := randomVector(dim)
	b.ResetTimer()
	for range b.N {
		bf.Search(query, 10)
	}
}
