package vecstore

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Limits for deserialization to prevent OOM from crafted inputs.
const (
	maxDeserializeDim      = 65536     // no embedding model produces >64K dims
	maxDeserializeCount    = 10_000_000 // 10M nodes
	maxDeserializeLevel    = 64        // HNSW levels beyond ~20 are unreachable in practice
	maxDeserializeStringLen = 512      // ULIDs are 26 chars; 512 is generous
)

// Binary format for HNSW persistence:
//
//	Header (16 bytes):
//	  magic    [4]byte  = "HNSW"
//	  version  uint32   = 1
//	  dim      uint32
//	  count    uint32
//
//	Per node:
//	  id_len   uint16
//	  id       [id_len]byte
//	  level    uint16
//	  vec      [dim]float32 (little-endian)
//	  per layer (level+1 times):
//	    num_friends uint16
//	    per friend:
//	      fid_len uint16
//	      fid     [fid_len]byte
//
//	Footer:
//	  entry_id_len uint16
//	  entry_id     [entry_id_len]byte
//	  max_level    uint16
//	  m            uint16
//	  ef_construct uint16

var hnswMagic = [4]byte{'H', 'N', 'S', 'W'}

// MarshalHNSW serializes an HNSW index to bytes.
func MarshalHNSW(h *HNSW) []byte {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Estimate size: header + nodes + footer.
	avgFriends := h.m * 2
	est := 16 + len(h.nodes)*(2+36+2+h.dim*4+(2+36)*avgFriends) + 2 + 36 + 6
	buf := make([]byte, 0, est)

	// Header.
	buf = append(buf, hnswMagic[:]...)
	buf = appendUint32(buf, 1) // version
	buf = appendUint32(buf, uint32(h.dim))
	buf = appendUint32(buf, uint32(len(h.nodes)))

	// Nodes.
	for _, node := range h.nodes {
		buf = appendString(buf, node.id)
		buf = appendUint16(buf, uint16(node.level))

		// Vector.
		for _, v := range node.vec {
			buf = appendUint32(buf, math.Float32bits(v))
		}

		// Friends per layer.
		for lc := 0; lc <= node.level; lc++ {
			friends := node.friends[lc]
			buf = appendUint16(buf, uint16(len(friends)))
			for _, fid := range friends {
				buf = appendString(buf, fid)
			}
		}
	}

	// Footer.
	buf = appendString(buf, h.entryID)
	buf = appendUint16(buf, uint16(h.maxLevel))
	buf = appendUint16(buf, uint16(h.m))
	buf = appendUint16(buf, uint16(h.efConstruction))

	return buf
}

// UnmarshalHNSW deserializes an HNSW index from bytes.
func UnmarshalHNSW(data []byte) (*HNSW, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("vecstore: data too short for HNSW header")
	}

	r := &reader{data: data}

	// Header.
	magic := r.readBytes(4)
	if magic == nil || magic[0] != hnswMagic[0] || magic[1] != hnswMagic[1] ||
		magic[2] != hnswMagic[2] || magic[3] != hnswMagic[3] {
		return nil, fmt.Errorf("vecstore: invalid HNSW magic bytes")
	}
	version := r.readUint32()
	if version != 1 {
		return nil, fmt.Errorf("vecstore: unsupported HNSW version %d", version)
	}
	dim := int(r.readUint32())
	if dim > maxDeserializeDim || dim <= 0 {
		return nil, fmt.Errorf("vecstore: dim %d out of valid range [1, %d]", dim, maxDeserializeDim)
	}
	count := int(r.readUint32())
	if count > maxDeserializeCount {
		return nil, fmt.Errorf("vecstore: count %d exceeds maximum %d", count, maxDeserializeCount)
	}

	if r.err != nil {
		return nil, fmt.Errorf("vecstore: read header: %w", r.err)
	}

	nodes := make(map[string]*hnswNode, count)

	for i := range count {
		if r.err != nil {
			return nil, fmt.Errorf("vecstore: read node %d: %w", i, r.err)
		}

		id := r.readString()
		level := int(r.readUint16())
		if level > maxDeserializeLevel {
			return nil, fmt.Errorf("vecstore: node %d level %d exceeds maximum %d", i, level, maxDeserializeLevel)
		}

		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = math.Float32frombits(r.readUint32())
		}

		friends := make([][]string, level+1)
		for lc := 0; lc <= level; lc++ {
			nf := int(r.readUint16())
			friends[lc] = make([]string, nf)
			for j := range nf {
				friends[lc][j] = r.readString()
			}
		}

		nodes[id] = &hnswNode{
			id:      id,
			vec:     vec,
			level:   level,
			friends: friends,
		}
	}

	if r.err != nil {
		return nil, fmt.Errorf("vecstore: read nodes: %w", r.err)
	}

	// Footer.
	entryID := r.readString()
	maxLevel := int(r.readUint16())
	m := int(r.readUint16())
	efConstruction := int(r.readUint16())

	if r.err != nil {
		return nil, fmt.Errorf("vecstore: read footer: %w", r.err)
	}

	return &HNSW{
		dim:            dim,
		m:              m,
		mMax0:          2 * m,
		efConstruction: efConstruction,
		ml:             1.0 / math.Log(float64(m)),
		nodes:          nodes,
		entryID:        entryID,
		maxLevel:       maxLevel,
	}, nil
}

// Binary encoding helpers.

func appendUint16(buf []byte, v uint16) []byte {
	return append(buf, byte(v), byte(v>>8))
}

func appendUint32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func appendString(buf []byte, s string) []byte {
	if len(s) > 0xFFFF {
		panic(fmt.Sprintf("vecstore: string too long for uint16 length: %d bytes", len(s)))
	}
	buf = appendUint16(buf, uint16(len(s)))
	return append(buf, s...)
}

// reader is a simple cursor over a byte slice.
type reader struct {
	data []byte
	pos  int
	err  error
}

func (r *reader) readBytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if r.pos+n > len(r.data) {
		r.err = fmt.Errorf("unexpected EOF at offset %d (need %d bytes)", r.pos, n)
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}

func (r *reader) readUint16() uint16 {
	b := r.readBytes(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) readUint32() uint32 {
	b := r.readBytes(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) readString() string {
	n := int(r.readUint16())
	if n > maxDeserializeStringLen {
		r.err = fmt.Errorf("string length %d exceeds maximum %d at offset %d", n, maxDeserializeStringLen, r.pos)
		return ""
	}
	b := r.readBytes(n)
	if b == nil {
		return ""
	}
	return string(b)
}
