package objects3d

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

type apngChunk struct {
	typ  string
	data []byte
}

func readChunks(t *testing.T, b []byte) []apngChunk {
	t.Helper()
	if len(b) < 8 || !bytes.Equal(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Fatal("missing PNG signature")
	}
	var out []apngChunk
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		data := b[i+8 : i+8+n]
		if crc32.ChecksumIEEE(b[i+4:i+8+n]) != binary.BigEndian.Uint32(b[i+8+n:]) {
			t.Fatalf("bad CRC on %s", typ)
		}
		out = append(out, apngChunk{typ, data})
		i += 12 + n
	}
	return out
}

// decodeAPNGFrames rebuilds each frame as a standalone PNG from the shared
// IHDR and the frame's IDAT/fdAT data, and decodes it.
func decodeAPNGFrames(t *testing.T, b []byte) ([]image.Image, [][2]uint16) {
	t.Helper()
	var ihdr []byte
	var frames [][]byte
	var delays [][2]uint16
	for _, c := range readChunks(t, b) {
		switch c.typ {
		case "IHDR":
			ihdr = c.data
		case "fcTL":
			delays = append(delays, [2]uint16{binary.BigEndian.Uint16(c.data[20:]), binary.BigEndian.Uint16(c.data[22:])})
			frames = append(frames, nil)
		case "IDAT":
			frames[len(frames)-1] = append(frames[len(frames)-1], c.data...)
		case "fdAT":
			frames[len(frames)-1] = append(frames[len(frames)-1], c.data[4:]...)
		}
	}
	var imgs []image.Image
	for i, f := range frames {
		var buf bytes.Buffer
		buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
		writeAPNGChunk(&buf, "IHDR", ihdr)
		writeAPNGChunk(&buf, "IDAT", f)
		writeAPNGChunk(&buf, "IEND", nil)
		img, err := png.Decode(&buf)
		if err != nil {
			t.Fatalf("frame %d does not decode with the shared IHDR: %v", i, err)
		}
		imgs = append(imgs, img)
	}
	return imgs, delays
}

// Frames that differ in opacity share one IHDR, so all must be encoded with
// the same colour type.
func TestEncodeAPNGMixedOpacity(t *testing.T) {
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 3))
	translucent := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			opaque.SetRGBA(x, y, color.RGBA{uint8(40 * x), uint8(60 * y), 7, 0xff})
		}
	}
	translucent.SetRGBA(1, 1, color.RGBA{0x80, 0, 0, 0x80})
	for _, order := range [][]*image.RGBA{{opaque, translucent}, {translucent, opaque}} {
		b, err := encodeAPNG(order, 90, 1000)
		if err != nil {
			t.Fatal(err)
		}
		imgs, _ := decodeAPNGFrames(t, b)
		if len(imgs) != 2 {
			t.Fatalf("decoded %d frames", len(imgs))
		}
		for i, want := range order {
			for y := 0; y < 3; y++ {
				for x := 0; x < 4; x++ {
					if got := color.RGBAModel.Convert(imgs[i].At(x, y)).(color.RGBA); got != want.RGBAAt(x, y) {
						t.Fatalf("frame %d pixel (%d,%d) = %v, want %v", i, x, y, got, want.RGBAAt(x, y))
					}
				}
			}
		}
	}
}

func TestEncodeAPNGRejectsMixedSizes(t *testing.T) {
	if _, err := encodeAPNG([]*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2)), image.NewRGBA(image.Rect(0, 0, 3, 2))}, 1, 10); err == nil {
		t.Error("frames of different sizes accepted")
	}
}

// Frame delays beyond 65535 ms keep their length with a coarser denominator.
func TestAPNGDelay(t *testing.T) {
	cases := []struct {
		ms       int
		num, den uint16
	}{
		{90, 90, 1000},
		{65535, 65535, 1000},
		{65536, 6554, 100},
		{70000, 7000, 100},
		{1000000, 10000, 10},
		{65535 * 1000, 65535, 1},
		{1 << 40, 65535, 1},
	}
	for _, c := range cases {
		if num, den := apngDelay(c.ms); num != c.num || den != c.den {
			t.Errorf("apngDelay(%d) = %d/%d, want %d/%d", c.ms, num, den, c.num, c.den)
		}
	}

	root := colouredCube(1000)
	m := &Model{Root: root, AllObjects: []*Object{root}}
	opts := DefaultRenderOptions()
	opts.UnitsPerPixel = 1
	b, err := m.RenderSpinAPNG(opts, 3, 70000)
	if err != nil {
		t.Fatal(err)
	}
	_, delays := decodeAPNGFrames(t, b)
	for i, d := range delays {
		if d != [2]uint16{7000, 100} {
			t.Errorf("frame %d delay = %d/%d, want 7000/100", i, d[0], d[1])
		}
	}
}
