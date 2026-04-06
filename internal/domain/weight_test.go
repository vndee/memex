package domain

import (
	"math"
	"testing"
)

func TestCombineWeights(t *testing.T) {
	tests := []struct {
		name string
		a, b float64
		want float64
	}{
		{"zero+zero", 0, 0, 0},
		{"one+anything", 1.0, 0.5, 1.0},
		{"anything+one", 0.3, 1.0, 1.0},
		{"half+half", 0.5, 0.5, 0.75},
		{"high+low", 0.8, 0.5, 0.90},
		{"symmetric", 0.3, 0.7, 0.79},
		{"zero+nonzero", 0, 0.6, 0.6},
		{"negative clamped", -0.5, 0.5, 0.5},
		{"over-one clamped", 1.5, 0.5, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CombineWeights(tt.a, tt.b)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("CombineWeights(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			// Verify symmetry.
			gotReverse := CombineWeights(tt.b, tt.a)
			if math.Abs(got-gotReverse) > 1e-10 {
				t.Errorf("CombineWeights is not symmetric: (%v,%v)=%v vs (%v,%v)=%v", tt.a, tt.b, got, tt.b, tt.a, gotReverse)
			}
		})
	}
}

func TestCombineWeightsMulti(t *testing.T) {
	got := CombineWeightsMulti([]float64{0.5, 0.5, 0.5})
	want := 0.875 // 1 - 0.5^3
	if math.Abs(got-want) > 0.001 {
		t.Errorf("CombineWeightsMulti([0.5,0.5,0.5]) = %v, want %v", got, want)
	}

	if CombineWeightsMulti(nil) != 0 {
		t.Error("CombineWeightsMulti(nil) should return 0")
	}

	if CombineWeightsMulti([]float64{0.7}) != 0.7 {
		t.Error("CombineWeightsMulti single element should return that element")
	}
}
