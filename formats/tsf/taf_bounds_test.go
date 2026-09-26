package tsf

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// sharedPixelTAF builds a TAF whose frames all use one frame info record and
// one w x h ARGB4444 pixel block.
func sharedPixelTAF(frames, w, h int) []byte {
	listOff := headerSize + 4 + sequenceHeaderLen
	infoOff := listOff + frames*frameListItemSize
	pixOff := infoOff + frameInfoSize
	data := make([]byte, pixOff+w*h*2)

	binary.LittleEndian.PutUint32(data[0:], Version)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], headerSize+4)

	seq := headerSize + 4
	binary.LittleEndian.PutUint16(data[seq:], uint16(frames))
	binary.LittleEndian.PutUint16(data[seq+2:], 1)
	copy(data[seq+8:], "shared")

	for i := 0; i < frames; i++ {
		item := listOff + i*frameListItemSize
		binary.LittleEndian.PutUint32(data[item:], uint32(infoOff))
		binary.LittleEndian.PutUint32(data[item+4:], 2)
	}

	binary.LittleEndian.PutUint16(data[infoOff:], uint16(w))
	binary.LittleEndian.PutUint16(data[infoOff+2:], uint16(h))
	data[infoOff+9] = uint8(FormatARGB4444)
	binary.LittleEndian.PutUint32(data[infoOff+16:], uint32(pixOff))

	for i := pixOff; i < len(data); i++ {
		data[i] = byte(i)
	}
	return data
}

// TestParseTAFRejectsReusedPixelsBeyondFileSize checks that frames pointing
// at one pixel block cannot make ParseTAF copy more pixel bytes than the file
// holds.
func TestParseTAFRejectsReusedPixelsBeyondFileSize(t *testing.T) {
	data := sharedPixelTAF(200, 64, 64) // 200 x 8 KiB of pixels from a ~10 KiB file
	_, err := ParseTAF(data)
	if err == nil || !strings.Contains(err.Error(), "file size") {
		t.Fatalf("ParseTAF: got %v, want an error about the file size", err)
	}
}

// TestParseTAFAcceptsReusedPixelsWithinFileSize checks that a small amount of
// reuse is still read, with each frame holding its own copy of the pixels.
func TestParseTAFAcceptsReusedPixelsWithinFileSize(t *testing.T) {
	data := sharedPixelTAF(2, 4, 4) // 2 x 32 pixel bytes in a 128-byte file
	taf, err := ParseTAF(data)
	if err != nil {
		t.Fatalf("ParseTAF: %v", err)
	}
	if len(taf.Frames) != 2 {
		t.Fatalf("frames: got %d, want 2", len(taf.Frames))
	}
	a, b := taf.Frames[0].Pixels, taf.Frames[1].Pixels
	if !bytes.Equal(a, b) || len(a) != 32 {
		t.Fatalf("frames should hold the same 32 pixel bytes: %x %x", a, b)
	}
	a[0] ^= 0xFF
	if a[0] == b[0] {
		t.Fatal("frames share one pixel buffer; each should have its own copy")
	}
}
