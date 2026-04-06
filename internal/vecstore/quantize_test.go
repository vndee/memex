package vecstore

import (
	"math"
	"testing"
)

func TestQuantize_Dequantize_RoundTrip(t *testing.T) {
	v := []float32{-0.5, 0.0, 0.5, 1.0, -1.0, 0.3, -0.8, 0.7}
	qv := Quantize(v)
	out := qv.Dequantize()

	if len(out) != len(v) {
		t.Fatalf("length mismatch: want %d, got %d", len(v), len(out))
	}

	for i := range v {
		diff := math.Abs(float64(v[i] - out[i]))
		if diff > 0.02 {
			t.Errorf("index %d: want ~%.4f, got %.4f (diff %.4f)", i, v[i], out[i], diff)
		}
	}
}

func TestQuantize_AllSame(t *testing.T) {
	v := []float32{0.5, 0.5, 0.5}
	qv := Quantize(v)
	for _, q := range qv.Data {
		if q != 0 {
			t.Errorf("constant vector should quantize to all zeros, got %d", q)
		}
	}
	out := qv.Dequantize()
	for i, x := range out {
		if math.Abs(float64(x-0.5)) > 0.01 {
			t.Errorf("index %d: want ~0.5, got %f", i, x)
		}
	}
}

func TestQuantize_Empty(t *testing.T) {
	qv := Quantize(nil)
	if len(qv.Data) != 0 {
		t.Errorf("empty input should produce empty quantized data")
	}
}

func TestQuantizedDotProduct_Accuracy(t *testing.T) {
	a := randomVector(768)
	b := randomVector(768)

	exactDot := DotProduct(a, b)
	qa := Quantize(a)
	approxDot := QuantizedDotProduct(qa, b)

	// Allow ~5% relative error.
	relErr := math.Abs(float64(exactDot-approxDot)) / math.Max(math.Abs(float64(exactDot)), 1e-6)
	if relErr > 0.10 {
		t.Errorf("quantized dot product relative error %.2f%% (exact=%.4f, approx=%.4f)",
			relErr*100, exactDot, approxDot)
	}
}

func TestQuantizedCosineDistance_Accuracy(t *testing.T) {
	a := randomVector(768)
	b := randomVector(768)

	exactDist := CosineDistance(a, b)
	qa := Quantize(a)
	bNorm := VectorNorm(b)
	approxDist := QuantizedCosineDistance(qa, b, bNorm)

	absDiff := math.Abs(float64(exactDist - approxDist))
	if absDiff > 0.05 {
		t.Errorf("quantized cosine distance diff %.4f (exact=%.4f, approx=%.4f)",
			absDiff, exactDist, approxDist)
	}
}

func BenchmarkQuantize_768(b *testing.B) {
	v := randomVector(768)
	b.ResetTimer()
	for range b.N {
		Quantize(v)
	}
}

func BenchmarkQuantizedCosineDistance_768(b *testing.B) {
	a := randomVector(768)
	query := randomVector(768)
	qa := Quantize(a)
	qNorm := VectorNorm(query)
	b.ResetTimer()
	for range b.N {
		QuantizedCosineDistance(qa, query, qNorm)
	}
}
