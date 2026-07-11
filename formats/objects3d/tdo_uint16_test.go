package objects3d

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestVertexIndexReadUnsigned verifies that primitive vertex indices are read
// as unsigned 16-bit values. Reading them signed would map any index above
// 32767 to a negative, wrong vertex.
func TestVertexIndexReadUnsigned(t *testing.T) {
	const (
		vertexOffset = 52 // after the 52-byte object header
		primOffset   = 64 // one 12-byte vertex follows the header
		indexOffset  = 96 // one 32-byte primitive follows the vertex
		bigIndex     = uint16(40000)
	)

	buf := make([]byte, indexOffset+2)
	le := binary.LittleEndian

	// rawObject header (13 int32 fields).
	le.PutUint32(buf[4:], 1)             // NumberOfVertexes
	le.PutUint32(buf[8:], 1)             // NumberOfPrimitives
	le.PutUint32(buf[36:], vertexOffset) // OffsetToVertexArray
	le.PutUint32(buf[40:], primOffset)   // OffsetToPrimitiveArray

	// rawPrimitive header (8 int32 fields), starting at primOffset.
	le.PutUint32(buf[primOffset+4:], 1)            // NumberOfVertexIndexes
	le.PutUint32(buf[primOffset+12:], indexOffset) // OffsetToVertexIndexArray

	// The single vertex index, above the signed 16-bit range.
	le.PutUint16(buf[indexOffset:], bigIndex)

	m, err := LoadFromReader(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	if len(m.Root.Primitives) != 1 {
		t.Fatalf("got %d primitives, want 1", len(m.Root.Primitives))
	}
	got := m.Root.Primitives[0].VertexIndices
	if len(got) != 1 {
		t.Fatalf("got %d vertex indices, want 1", len(got))
	}
	if got[0] != int(bigIndex) {
		t.Errorf("vertex index: got %d, want %d (unsigned read)", got[0], bigIndex)
	}
}
