package vecstore

import "math"

// CosineSimilarity returns the cosine similarity between two vectors.
// Returns a value in [-1, 1] where 1 means identical direction.
// Both vectors must have the same length; panics otherwise.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		panic("vecstore: vectors must have equal length")
	}
	if len(a) == 0 {
		return 0
	}

	var dot, normA, normB float32

	// Process 8 elements at a time for better CPU pipelining.
	n := len(a)
	limit := n - (n % 8)

	for i := 0; i < limit; i += 8 {
		a0, a1, a2, a3 := a[i], a[i+1], a[i+2], a[i+3]
		a4, a5, a6, a7 := a[i+4], a[i+5], a[i+6], a[i+7]
		b0, b1, b2, b3 := b[i], b[i+1], b[i+2], b[i+3]
		b4, b5, b6, b7 := b[i+4], b[i+5], b[i+6], b[i+7]

		dot += a0*b0 + a1*b1 + a2*b2 + a3*b3 + a4*b4 + a5*b5 + a6*b6 + a7*b7
		normA += a0*a0 + a1*a1 + a2*a2 + a3*a3 + a4*a4 + a5*a5 + a6*a6 + a7*a7
		normB += b0*b0 + b1*b1 + b2*b2 + b3*b3 + b4*b4 + b5*b5 + b6*b6 + b7*b7
	}

	for i := limit; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(normA)*float64(normB)))
}

// CosineDistance returns 1 - CosineSimilarity, in [0, 2].
// Suitable as a distance metric where 0 = identical.
func CosineDistance(a, b []float32) float32 {
	return 1 - CosineSimilarity(a, b)
}

// L2Distance returns the Euclidean distance between two vectors.
func L2Distance(a, b []float32) float32 {
	return float32(math.Sqrt(float64(l2SumSquared(a, b))))
}

// L2DistanceSquared returns the squared Euclidean distance (avoids sqrt).
// Useful for comparisons where only relative ordering matters.
func L2DistanceSquared(a, b []float32) float32 {
	return l2SumSquared(a, b)
}

// l2SumSquared computes the sum of squared differences, loop-unrolled 8-wide.
func l2SumSquared(a, b []float32) float32 {
	if len(a) != len(b) {
		panic("vecstore: vectors must have equal length")
	}

	var sum float32
	n := len(a)
	limit := n - (n % 8)

	for i := 0; i < limit; i += 8 {
		d0 := a[i] - b[i]
		d1 := a[i+1] - b[i+1]
		d2 := a[i+2] - b[i+2]
		d3 := a[i+3] - b[i+3]
		d4 := a[i+4] - b[i+4]
		d5 := a[i+5] - b[i+5]
		d6 := a[i+6] - b[i+6]
		d7 := a[i+7] - b[i+7]
		sum += d0*d0 + d1*d1 + d2*d2 + d3*d3 + d4*d4 + d5*d5 + d6*d6 + d7*d7
	}

	for i := limit; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}

	return sum
}

// DotProduct returns the dot product of two vectors.
func DotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		panic("vecstore: vectors must have equal length")
	}

	var dot float32
	n := len(a)
	limit := n - (n % 8)

	for i := 0; i < limit; i += 8 {
		dot += a[i]*b[i] + a[i+1]*b[i+1] + a[i+2]*b[i+2] + a[i+3]*b[i+3] +
			a[i+4]*b[i+4] + a[i+5]*b[i+5] + a[i+6]*b[i+6] + a[i+7]*b[i+7]
	}

	for i := limit; i < n; i++ {
		dot += a[i] * b[i]
	}

	return dot
}

// VectorNorm returns the L2 norm of a vector.
func VectorNorm(v []float32) float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	return float32(math.Sqrt(float64(sum)))
}

// Normalize returns a unit-length copy of v. Returns a zero vector if v has zero magnitude.
func Normalize(v []float32) []float32 {
	out := make([]float32, len(v))
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return out
	}
	inv := float32(1.0 / math.Sqrt(float64(sum)))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}
