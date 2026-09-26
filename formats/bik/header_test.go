package bik

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// synthHeader returns a 44-byte Bink header with no audio tracks.
func synthHeader(fileSizeMinus8, fpsNum, fpsDen uint32) []byte {
	var b bytes.Buffer
	b.WriteString("BIKf")
	for _, v := range []uint32{fileSizeMinus8, 10, 100, 10, 320, 240, fpsNum, fpsDen, 0, 0} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}

func TestReadHeaderRejectsBadFields(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"file size wraps", synthHeader(0xFFFFFFFC, 15, 1), "file size"},
		{"zero fps numerator", synthHeader(5000, 0, 1), "frame rate"},
		{"zero fps denominator", synthHeader(5000, 15, 0), "frame rate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ReadHeader(bytes.NewReader(c.data))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error about the %s", err, c.want)
			}
		})
	}

	h, err := ReadHeader(bytes.NewReader(synthHeader(0xFFFFFFF7, 30000, 1001)))
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if h.FileSize != 0xFFFFFFFF {
		t.Errorf("FileSize = 0x%X, want 0xFFFFFFFF", h.FileSize)
	}
	r := &Reader{header: h}
	if r.FrameRate() < 29.97 || r.FrameRate() > 29.98 || r.Duration() <= 0 {
		t.Errorf("rate %v duration %v", r.FrameRate(), r.Duration())
	}
}
