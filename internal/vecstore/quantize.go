package vecstore

import "math"

// QuantizedVector holds an int8-quantized vector with scale/offset for reconstruction.
// Original value ≈ (int8_value * scale) + offset
type QuantizedVector struct {
	Data   []int8
	Scale  float32
	Offset float32
	Norm   float32 // precomputed L2 norm of dequantized vector
}

// Quantize converts a float32 vector to int8 using min-max scalar quantization.
// Maps [min, max] to [-127, 127]. Achieves ~4x memory reduction with <1% accuracy loss
// on normalized embedding vectors.
func Quantize(v []float32) QuantizedVector {
	if len(v) == 0 {
		return QuantizedVector{}
	}

	min, max := v[0], v[0]
	for _, x := range v[1:] {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}

	rng := max - min
	if rng == 0 {
		return QuantizedVector{
			Data:   make([]int8, len(v)),
			Scale:  0,
			Offset: min,
		}
	}

	scale := rng / 254.0 // map full range to [-127, 127]
	offset := min + 127.0*scale

	data := make([]int8, len(v))
	invScale := 1.0 / scale
	for i, x := range v {
		q := (x - offset) * invScale
		if q > 127 {
			q = 127
		} else if q < -127 {
			q = -127
		}
		data[i] = int8(math.RoundToEven(float64(q)))
	}

	// Precompute the dequantized vector's L2 norm.
	var normSq float32
	for _, q := range data {
		f := float32(q)*scale + offset
		normSq += f * f
	}

	return QuantizedVector{
		Data:   data,
		Scale:  scale,
		Offset: offset,
		Norm:   float32(math.Sqrt(float64(normSq))),
	}
}

// Dequantize reconstructs a float32 vector from quantized form.
func (qv QuantizedVector) Dequantize() []float32 {
	out := make([]float32, len(qv.Data))
	for i, q := range qv.Data {
		out[i] = float32(q)*qv.Scale + qv.Offset
	}
	return out
}

// QuantizedDotProduct computes an approximate dot product between a quantized
// stored vector and a float32 query vector without full dequantization.
//
//	dot(dequant(q), b) = scale * sum(q_i * b_i) + offset * sum(b_i)
func QuantizedDotProduct(qv QuantizedVector, b []float32) float32 {
	if len(qv.Data) != len(b) {
		panic("vecstore: vectors must have equal length")
	}

	var qDot, bSum float32

	n := len(qv.Data)
	limit := n - (n % 8)

	for i := 0; i < limit; i += 8 {
		qDot += float32(qv.Data[i])*b[i] + float32(qv.Data[i+1])*b[i+1] +
			float32(qv.Data[i+2])*b[i+2] + float32(qv.Data[i+3])*b[i+3] +
			float32(qv.Data[i+4])*b[i+4] + float32(qv.Data[i+5])*b[i+5] +
			float32(qv.Data[i+6])*b[i+6] + float32(qv.Data[i+7])*b[i+7]
		bSum += b[i] + b[i+1] + b[i+2] + b[i+3] +
			b[i+4] + b[i+5] + b[i+6] + b[i+7]
	}
	for i := limit; i < n; i++ {
		qDot += float32(qv.Data[i]) * b[i]
		bSum += b[i]
	}

	return qv.Scale*qDot + qv.Offset*bSum
}

// QuantizedCosineDistance computes approximate cosine distance using a quantized
// stored vector and a float32 query. queryNorm should be precomputed for batch efficiency.
// Uses the precomputed Norm field instead of recomputing per call.
func QuantizedCosineDistance(qv QuantizedVector, query []float32, queryNorm float32) float32 {
	if queryNorm == 0 || qv.Norm == 0 {
		return 1
	}

	dot := QuantizedDotProduct(qv, query)
	sim := dot / (qv.Norm * queryNorm)
	return 1 - sim
}

