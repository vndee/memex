package vecstore

import "math/rand/v2"

func randomVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rand.Float32()*2 - 1
	}
	return v
}

func randomVectors(n, dim int) map[string][]float32 {
	vecs := make(map[string][]float32, n)
	for i := range n {
		id := "v" + string(rune('0'+i/1000)) + string(rune('0'+(i/100)%10)) +
			string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
		vecs[id] = randomVector(dim)
	}
	return vecs
}
