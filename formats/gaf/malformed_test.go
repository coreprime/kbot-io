package gaf

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrameByteCountRejectsOversizedDimensions(t *testing.T) {
	if _, err := frameByteCount(65535, 65535); err == nil {
		t.Fatal("expected error for 65535x65535 dimensions, got nil")
	}

	// The largest real TA/TA:K frames are 640x480; that must still be accepted.
	got, err := frameByteCount(640, 480)
	if err != nil {
		t.Fatalf("unexpected error for 640x480: %v", err)
	}
	if got != 640*480 {
		t.Fatalf("expected %d bytes, got %d", 640*480, got)
	}
}

// craftedGAF builds a minimal but structurally valid GAF whose single frame
// declares 65535x65535 dimensions, mimicking a malicious header that would
// force a ~4 GB allocation without the dimension cap.
func craftedGAF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatalf("failed to encode crafted GAF: %v", err)
		}
	}

	write(Header{Version: VersionTA, SequenceCount: 1}) // 12 bytes
	write(uint32(16))                                   // sequence pointer -> offset 16
	write(SequenceHeader{FrameCount: 1})                // 40 bytes at offset 16
	write(FrameListItem{PtrFrameInfo: 64})              // 8 bytes at offset 56
	write(FrameInfo{Width: 65535, Height: 65535, PtrFrameData: 88})
	return buf.Bytes()
}

func TestReadSequencesRejectsOversizedFrame(t *testing.T) {
	r, err := LoadFromReader(bytes.NewReader(craftedGAF(t)))
	if err != nil {
		t.Fatalf("failed to load crafted GAF: %v", err)
	}

	_, err = r.ReadSequences()
	if err == nil {
		t.Fatal("expected error reading oversized frame, got nil")
	}
	if !strings.Contains(err.Error(), "exceed maximum") {
		t.Fatalf("expected dimension-cap error, got: %v", err)
	}
}
