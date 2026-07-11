package pcx

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func TestDecodeRealAsset(t *testing.T) {
	path := testutil.UnpackedFile(t, "bitmaps", "battleroom.pcx")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read asset: %v", err)
	}

	reader, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to load PCX: %v", err)
	}

	img, err := reader.Decode()
	if err != nil {
		t.Fatalf("failed to decode PCX: %v", err)
	}

	b := img.Bounds()
	if b.Dx() != reader.Width() || b.Dy() != reader.Height() {
		t.Fatalf("decoded bounds %dx%d disagree with header %dx%d",
			b.Dx(), b.Dy(), reader.Width(), reader.Height())
	}
	if b.Dx() != 640 || b.Dy() != 480 {
		t.Fatalf("expected 640x480, got %dx%d", b.Dx(), b.Dy())
	}
}

// craftedPCX writes a 128-byte PCX header with the given extents.
func craftedPCX(xMin, yMin, xMax, yMax uint16) []byte {
	h := Header{
		Manufacturer: 0x0A,
		Encoding:     1,
		BitsPerPixel: 8,
		XMin:         xMin,
		YMin:         yMin,
		XMax:         xMax,
		YMax:         yMax,
		NumPlanes:    1,
		BytesPerLine: 1,
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, h)
	return buf.Bytes()
}

func TestDecodeRejectsUnderflowDimensions(t *testing.T) {
	// XMax < XMin would underflow the uint16 subtraction in Width().
	reader, err := LoadFromReader(bytes.NewReader(craftedPCX(100, 0, 0, 0)))
	if err != nil {
		t.Fatalf("failed to load crafted PCX: %v", err)
	}
	if _, err := reader.Decode(); err == nil || !strings.Contains(err.Error(), "invalid PCX dimensions") {
		t.Fatalf("expected underflow error, got: %v", err)
	}
}

func TestDecodeRejectsOversizedDimensions(t *testing.T) {
	// 60000x60000 (~3.6 G pixels) is far above the ceiling but avoids the
	// uint16 wrap that XMax=65535 would hit in the (XMax-XMin+1) computation.
	reader, err := LoadFromReader(bytes.NewReader(craftedPCX(0, 0, 60000, 60000)))
	if err != nil {
		t.Fatalf("failed to load crafted PCX: %v", err)
	}
	if _, err := reader.Decode(); err == nil || !strings.Contains(err.Error(), "exceed maximum") {
		t.Fatalf("expected dimension-cap error, got: %v", err)
	}
}
