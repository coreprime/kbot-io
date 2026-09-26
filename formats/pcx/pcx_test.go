package pcx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/gaf"
	"github.com/coreprime/kbot-io/palettes"
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
	if reader.Truncated() {
		t.Error("retail image reported as truncated")
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
	// XMax < XMin gives a non-positive width.
	reader, err := LoadFromReader(bytes.NewReader(craftedPCX(100, 0, 0, 0)))
	if err != nil {
		t.Fatalf("failed to load crafted PCX: %v", err)
	}
	if _, err := reader.Decode(); err == nil || !strings.Contains(err.Error(), "invalid PCX dimensions") {
		t.Fatalf("expected underflow error, got: %v", err)
	}
}

func TestDecodeRejectsOversizedDimensions(t *testing.T) {
	// 60000x60000 (~3.6 G pixels) is far above the ceiling.
	reader, err := LoadFromReader(bytes.NewReader(craftedPCX(0, 0, 60000, 60000)))
	if err != nil {
		t.Fatalf("failed to load crafted PCX: %v", err)
	}
	if _, err := reader.Decode(); err == nil || !strings.Contains(err.Error(), "exceed maximum") {
		t.Fatalf("expected dimension-cap error, got: %v", err)
	}
}

// pcxSpec describes a synthetic PCX file. Zero fields take the values of a
// plain 8-bit, single-plane, RLE, version 5 file.
type pcxSpec struct {
	version, encoding, bpp, planes byte
	width, height                  int // sets XMax/YMax (and BytesPerLine when bpl is 0) when non-zero
	xMin, yMin, xMax, yMax         uint16
	bpl                            uint16
	pixels                         []byte // encoded pixel data
	noMarker                       bool   // write 0x00 instead of the 0x0C marker
	noColorMap                     bool   // write neither marker nor colour map
}

// testColorMap is a recognisable 768-byte colour map: entry i is (i, 255-i, i/2).
func testColorMap() []byte {
	m := make([]byte, ColorMapSize)
	for i := 0; i < 256; i++ {
		m[i*3], m[i*3+1], m[i*3+2] = byte(i), byte(255-i), byte(i/2)
	}
	return m
}

func buildPCX(t *testing.T, s pcxSpec) []byte {
	t.Helper()
	h := Header{
		Manufacturer: 0x0A,
		Version:      5,
		Encoding:     1,
		BitsPerPixel: 8,
		NumPlanes:    1,
		XMin:         s.xMin,
		YMin:         s.yMin,
		XMax:         s.xMax,
		YMax:         s.yMax,
		BytesPerLine: s.bpl,
	}
	if s.version != 0 {
		h.Version = s.version
	}
	if s.encoding != 0 {
		h.Encoding = s.encoding
	}
	if s.bpp != 0 {
		h.BitsPerPixel = s.bpp
	}
	if s.planes != 0 {
		h.NumPlanes = s.planes
	}
	if s.width > 0 {
		h.XMax = s.xMin + uint16(s.width-1)
		h.YMax = s.yMin + uint16(s.height-1)
		if s.bpl == 0 {
			h.BytesPerLine = uint16(s.width)
		}
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, h); err != nil {
		t.Fatal(err)
	}
	buf.Write(s.pixels)
	if !s.noColorMap {
		if s.noMarker {
			buf.WriteByte(0x00)
		} else {
			buf.WriteByte(PaletteMarker)
		}
		buf.Write(testColorMap())
	}
	return buf.Bytes()
}

func load(t *testing.T, data []byte) *Reader {
	t.Helper()
	r, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	return r
}

// pixels returns the rows of a paletted image without stride padding.
func pixels(t *testing.T, img image.Image) []byte {
	t.Helper()
	p, ok := img.(*image.Paletted)
	if !ok {
		t.Fatalf("expected *image.Paletted, got %T", img)
	}
	var out []byte
	for y := 0; y < p.Rect.Dy(); y++ {
		out = append(out, p.Pix[y*p.Stride:y*p.Stride+p.Rect.Dx()]...)
	}
	return out
}

func TestWidthHeightUseFullIntegers(t *testing.T) {
	// XMax=65535 with XMin=0 is 65,536 pixels wide; it must not wrap to 0.
	r := load(t, buildPCX(t, pcxSpec{xMax: 65535, yMax: 0, bpl: 65535}))
	if r.Width() != 65536 || r.Height() != 1 {
		t.Fatalf("Width/Height = %d/%d, want 65536/1", r.Width(), r.Height())
	}
	inv := load(t, buildPCX(t, pcxSpec{xMin: 10, xMax: 2}))
	if inv.Width() > 0 {
		t.Fatalf("inverted bounds should give a non-positive width, got %d", inv.Width())
	}
}

// fullWidthPCX is a 65,536x1 image of value 7: 1,041 runs of 63, the last
// one clipped at the row end.
func fullWidthPCX(t *testing.T) []byte {
	var enc []byte
	for i := 0; i < 1041; i++ {
		enc = append(enc, 0xFF, 7)
	}
	return buildPCX(t, pcxSpec{xMax: 65535, bpl: 65535, pixels: enc})
}

func TestDecodeFullWidthImage(t *testing.T) {
	r := load(t, fullWidthPCX(t))
	img, err := r.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 65536 || b.Dy() != 1 {
		t.Fatalf("bounds %v, want 65536x1", b)
	}
	px := pixels(t, img)
	if px[0] != 7 || px[65535] != 7 || r.Truncated() {
		t.Fatalf("first/last pixel %d/%d truncated=%v", px[0], px[65535], r.Truncated())
	}
}

func TestStandardBytesPerLine(t *testing.T) {
	// A 3x2 image padded to BytesPerLine 4 (pad byte 9): the padding is
	// skipped.
	data := buildPCX(t, pcxSpec{width: 3, height: 2, bpl: 4, pixels: []byte{1, 2, 3, 9, 4, 5, 6, 9}})
	img, err := load(t, data).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, img); !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6}) {
		t.Errorf("rows = %v, want [1 2 3 4 5 6]", got)
	}

	// BytesPerLine 0 (below the width) used to give an all-zero image; the
	// width is used instead.
	data = buildPCX(t, pcxSpec{xMax: 1, yMax: 1, bpl: 0, pixels: []byte{1, 2, 3, 4}})
	img, err = load(t, data).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, img); !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Errorf("rows = %v, want [1 2 3 4]", got)
	}
}

func TestStandardPalettesAreOpaque(t *testing.T) {
	// Index 0 is opaque whether the colours come from the file or, without a
	// marker, from the built-in TA palette.
	for _, noMarker := range []bool{false, true} {
		data := buildPCX(t, pcxSpec{width: 1, height: 1, pixels: []byte{0}, noMarker: noMarker})
		img, err := load(t, data).Decode()
		if err != nil {
			t.Fatal(err)
		}
		pal := img.(*image.Paletted).Palette
		for i, c := range pal {
			if _, _, _, a := c.RGBA(); a != 0xFFFF {
				t.Fatalf("noMarker=%v: palette index %d is not opaque", noMarker, i)
			}
		}
		if noMarker {
			ta := palettes.DefaultPalette
			if c := pal[1].(color.RGBA); c != (color.RGBA{ta[4], ta[5], ta[6], 255}) {
				t.Errorf("palette[1] = %v, want the TA palette", c)
			}
		}
	}
}

func TestEmbeddedPaletteFollowsGAFConvention(t *testing.T) {
	r := load(t, buildPCX(t, pcxSpec{width: 1, height: 1, pixels: []byte{0}}))
	got := r.EmbeddedPalette()
	if got == nil {
		t.Fatal("EmbeddedPalette returned nil")
	}
	m := testColorMap()
	entries := make([]byte, 1024)
	for i := 0; i < 256; i++ {
		copy(entries[i*4:], m[i*3:i*3+3])
	}
	want, err := gaf.LoadPaletteFromBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	if got.Colors != want.Colors {
		t.Error("EmbeddedPalette differs from gaf.LoadPaletteFromBytes on the same colours")
	}
	if load(t, buildPCX(t, pcxSpec{width: 1, height: 1, pixels: []byte{0}, noMarker: true})).EmbeddedPalette() != nil {
		t.Error("EmbeddedPalette should be nil without a marker")
	}
}

func TestMarkerMustFollowHeader(t *testing.T) {
	// 800 bytes: len-769 is offset 31, inside the header's EGA palette.
	data := make([]byte, 800)
	copy(data, buildPCX(t, pcxSpec{width: 1, height: 1, noColorMap: true}))
	data[len(data)-ColorMapSize-1] = PaletteMarker
	if load(t, data).HasEmbeddedPalette() {
		t.Error("a 0x0C byte inside the header was taken as the palette marker")
	}
}

func TestTruncatedPixelData(t *testing.T) {
	// A 2x3 image with one and a half rows of data and no colour map.
	data := buildPCX(t, pcxSpec{width: 2, height: 3, pixels: []byte{1, 2, 3}, noColorMap: true})
	r := load(t, data)
	img, err := r.Decode()
	if err != nil {
		t.Fatalf("8-bit decode should tolerate truncation: %v", err)
	}
	if !r.Truncated() {
		t.Error("Truncated() = false after a truncated decode")
	}
	if got := pixels(t, img); !bytes.Equal(got, []byte{1, 2, 3, 0, 0, 0}) {
		t.Errorf("rows = %v, want the decoded bytes then zeros", got)
	}

	complete := load(t, buildPCX(t, pcxSpec{width: 2, height: 1, pixels: []byte{1, 2}}))
	if _, err := complete.Decode(); err != nil || complete.Truncated() {
		t.Errorf("complete file: err=%v truncated=%v", err, complete.Truncated())
	}

	rgb := load(t, buildPCX(t, pcxSpec{planes: 3, width: 2, height: 2, pixels: []byte{1, 2, 3, 4}, noColorMap: true}))
	if _, err := rgb.Decode(); !errors.Is(err, ErrTruncated) || !rgb.Truncated() {
		t.Errorf("24-bit truncated decode: err=%v truncated=%v, want ErrTruncated", err, rgb.Truncated())
	}
}

func TestRunClippingAndZeroLengthRuns(t *testing.T) {
	// Row 0: a zero-length run (consumes 0x55), then a run of 5 sevens clipped
	// to the 3-pixel row. Row 1: literals 1 2 3.
	data := buildPCX(t, pcxSpec{width: 3, height: 2, pixels: []byte{0xC0, 0x55, 0xC5, 7, 1, 2, 3}})
	img, err := load(t, data).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, img); !bytes.Equal(got, []byte{7, 7, 7, 1, 2, 3}) {
		t.Errorf("rows = %v, want [7 7 7 1 2 3]", got)
	}
}

func TestDecodeIsRepeatable(t *testing.T) {
	r := load(t, buildPCX(t, pcxSpec{width: 2, height: 1, pixels: []byte{8, 9}}))
	a, err := r.Decode()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels(t, a), pixels(t, b)) || r.Truncated() {
		t.Errorf("second Decode = %v, first = %v, truncated=%v", pixels(t, b), pixels(t, a), r.Truncated())
	}
}
