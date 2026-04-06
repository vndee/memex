package vecstore

import (
	"math"
	"testing"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	got := CosineSimilarity(a, a)
	if math.Abs(float64(got)-1.0) > 1e-6 {
		t.Errorf("identical vectors: want ~1.0, got %f", got)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0, 0}
	b := []float32{0, 1, 0, 0}
	got := CosineSimilarity(a, b)
	if math.Abs(float64(got)) > 1e-6 {
		t.Errorf("orthogonal vectors: want ~0.0, got %f", got)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := CosineSimilarity(a, b)
	if math.Abs(float64(got)+1.0) > 1e-6 {
		t.Errorf("opposite vectors: want ~-1.0, got %f", got)
	}
}

func TestCosineDistance(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	got := CosineDistance(a, a)
	if math.Abs(float64(got)) > 1e-6 {
		t.Errorf("identical vectors distance: want ~0.0, got %f", got)
	}
}

func TestL2Distance(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{3, 4, 0}
	got := L2Distance(a, b)
	if math.Abs(float64(got)-5.0) > 1e-5 {
		t.Errorf("want 5.0, got %f", got)
	}
}

func TestL2DistanceSquared(t *testing.T) {
	a := []float32{0, 0}
	b := []float32{3, 4}
	got := L2DistanceSquared(a, b)
	if math.Abs(float64(got)-25.0) > 1e-5 {
		t.Errorf("want 25.0, got %f", got)
	}
}

func TestDotProduct(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	b := []float32{8, 7, 6, 5, 4, 3, 2, 1}
	got := DotProduct(a, b)
	want := float32(8 + 14 + 18 + 20 + 20 + 18 + 14 + 8)
	if math.Abs(float64(got-want)) > 1e-4 {
		t.Errorf("want %f, got %f", want, got)
	}
}

func TestNormalize(t *testing.T) {
	v := []float32{3, 4}
	n := Normalize(v)
	norm := VectorNorm(n)
	if math.Abs(float64(norm)-1.0) > 1e-6 {
		t.Errorf("normalized vector norm: want 1.0, got %f", norm)
	}
}

func TestNormalize_ZeroVector(t *testing.T) {
	v := []float32{0, 0, 0}
	n := Normalize(v)
	for i, x := range n {
		if x != 0 {
			t.Errorf("index %d: want 0, got %f", i, x)
		}
	}
}

// Test that the unrolled loop handles non-multiple-of-8 lengths.
func TestCosineSimilarity_OddLength(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5}
	b := []float32{5, 4, 3, 2, 1}
	got := CosineSimilarity(a, b)
	if got < 0.5 || got > 1.0 {
		t.Errorf("unexpected similarity for positively correlated vectors: %f", got)
	}
}

func BenchmarkCosineSimilarity_768(b *testing.B) {
	a := randomVector(768)
	c := randomVector(768)
	b.ResetTimer()
	for range b.N {
		CosineSimilarity(a, c)
	}
}

func BenchmarkCosineDistance_768(b *testing.B) {
	a := randomVector(768)
	c := randomVector(768)
	b.ResetTimer()
	for range b.N {
		CosineDistance(a, c)
	}
}

func BenchmarkL2Distance_768(b *testing.B) {
	a := randomVector(768)
	c := randomVector(768)
	b.ResetTimer()
	for range b.N {
		L2Distance(a, c)
	}
}

func BenchmarkDotProduct_768(b *testing.B) {
	a := randomVector(768)
	c := randomVector(768)
	b.ResetTimer()
	for range b.N {
		DotProduct(a, c)
	}
}
