package fnt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

// testGlyph describes one synthetic glyph: rows of '#' (set) and '.' (clear).
type testGlyph struct {
	ch   int
	rows []string
}

// buildFont assembles an FNT image with the given header bytes, a table of
// 256 - first entries and the glyph records appended after the table.
func buildFont(height, byte1 byte, baseline int8, first int, glyphs ...testGlyph) []byte {
	entries := 256 - first
	out := make([]byte, headerSize+2*entries)
	out[0], out[1], out[2], out[3] = height, byte1, byte(baseline), byte(first)
	for _, g := range glyphs {
		binary.LittleEndian.PutUint16(out[headerSize+2*(g.ch-first):], uint16(len(out)))
		w := len(g.rows[0])
		out = append(out, byte(w))
		bits := make([]byte, (w*len(g.rows)+7)/8)
		i := 0
		for _, row := range g.rows {
			for _, c := range row {
				if c == '#' {
					bits[i>>3] |= 0x80 >> uint(i&7)
				}
				i++
			}
		}
		out = append(out, bits...)
	}
	return out
}

// block returns a glyph of the given size whose left column is set.
func block(ch, w, h int) testGlyph {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = "#" + strings.Repeat(".", w-1)
	}
	return testGlyph{ch: ch, rows: rows}
}

func mustParse(t *testing.T, data []byte) *Font {
	t.Helper()
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestHeaderBytes(t *testing.T) {
	// Byte 1 is not part of the height; byte 2 is a signed baseline and byte 3
	// the first character code.
	f := mustParse(t, buildFont(11, 0x01, -2, 0))
	if f.Height != 11 {
		t.Errorf("Height = %d, want 11 (byte 0 only)", f.Height)
	}
	if f.Baseline != -2 {
		t.Errorf("Baseline = %d, want -2", f.Baseline)
	}
	if f.FirstChar != 0 {
		t.Errorf("FirstChar = %d, want 0", f.FirstChar)
	}
	if f.Flags != 0x00FE {
		t.Errorf("Flags = 0x%04X, want 0x00FE", f.Flags)
	}

	if _, err := Parse(buildFont(0, 0, 1, 0)); err == nil {
		t.Error("height 0 accepted")
	}
	if _, err := Parse([]byte{11, 0, 1}); err == nil {
		t.Error("3-byte header accepted")
	}
	if _, err := Parse(buildFont(11, 0, 1, 0)[:300]); err == nil {
		t.Error("truncated offset table accepted")
	}
}

func TestFirstCharShortensTable(t *testing.T) {
	data := buildFont(3, 0, 1, 'A', testGlyph{ch: 'A', rows: []string{"#.", ".#", "##"}})
	if len(data) != headerSize+2*(256-'A')+1+1 {
		t.Fatalf("unexpected synthetic size %d", len(data))
	}
	f := mustParse(t, data)
	if f.FirstChar != 'A' {
		t.Fatalf("FirstChar = %d, want %d", f.FirstChar, 'A')
	}
	g := f.Glyphs['A']
	if g == nil || g.Char != 'A' || g.Width != 2 || g.Height != 3 {
		t.Fatalf("glyph A = %+v", g)
	}
	want := []bool{true, false, false, true, true, true}
	for i, p := range want {
		if g.Pixels[i] != p {
			t.Fatalf("pixel %d = %v, want %v", i, g.Pixels[i], p)
		}
	}
	for c := 0; c < 'A'; c++ {
		if f.Glyphs[c] != nil {
			t.Fatalf("glyph 0x%02X below the first code is present", c)
		}
	}
	if len(f.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", f.Warnings)
	}
}

func TestPreambleOffsetDecodedWithWarning(t *testing.T) {
	// The entry for 'B' points at header byte 2, so the baseline (1) reads
	// as the glyph width and the first-code byte as its 2-row bitmap.
	data := buildFont(2, 0, 1, 0)
	binary.LittleEndian.PutUint16(data[headerSize+2*'B':], 2)
	f := mustParse(t, data)
	g := f.Glyphs['B']
	if g == nil || g.Width != 1 {
		t.Fatalf("glyph B = %+v, want a 1-pixel-wide glyph read from the header", g)
	}
	if len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0], "0x42") {
		t.Errorf("warnings = %v, want one naming glyph 0x42", f.Warnings)
	}
}

func TestBadGlyphsSkippedIndividually(t *testing.T) {
	data := buildFont(4, 0, 1, 0, block('A', 3, 4), block('Z', 200, 4))
	table := func(ch int, off int) { binary.LittleEndian.PutUint16(data[headerSize+2*ch:], uint16(off)) }
	table('B', 0xFFF0) // past the end
	widthZero := len(data)
	data = append(data, 0) // width 0
	table('C', widthZero)
	truncated := len(data)
	data = append(data, 8, 0xFF) // width 8 needs 4 bytes of bitmap
	table('D', truncated)

	f := mustParse(t, data)
	if f.Glyphs['A'] == nil || f.Glyphs['Z'] == nil {
		t.Fatal("good glyphs lost")
	}
	if f.Glyphs['Z'].Width != 200 {
		t.Errorf("wide glyph width = %d, want 200", f.Glyphs['Z'].Width)
	}
	for _, c := range "BCD" {
		if f.Glyphs[c] != nil {
			t.Errorf("glyph %c decoded, want skipped", c)
		}
	}
	if len(f.Warnings) != 3 {
		t.Errorf("warnings = %v, want 3", f.Warnings)
	}
}

func TestLoadFromEmbeddedStream(t *testing.T) {
	font := buildFont(5, 0, 2, 0, block('A', 4, 5), block('b', 2, 5))
	stream := append([]byte("prefix junk"), font...)
	r := bytes.NewReader(stream)
	if _, err := r.Seek(int64(len("prefix junk")), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFromReader(r)
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	want := mustParse(t, font)
	for c := range want.Glyphs {
		a, b := f.Glyphs[c], want.Glyphs[c]
		if (a == nil) != (b == nil) || (a != nil && (a.Width != b.Width || !equalPixels(a.Pixels, b.Pixels))) {
			t.Fatalf("glyph 0x%02X differs from the standalone parse", c)
		}
	}
}

func equalPixels(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// failingReader returns data and then a non-EOF error.
type failingReader struct {
	*bytes.Reader
	err error
}

func (r failingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func TestLoadReportsReadErrors(t *testing.T) {
	boom := errors.New("disk on fire")
	r := failingReader{bytes.NewReader(buildFont(5, 0, 1, 0, block('A', 2, 5))), boom}
	if _, err := LoadFromReader(r); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error", err)
	}
}

// endlessReader yields zero bytes forever and counts them.
type endlessReader struct{ n *int }

func (r endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	*r.n += len(p)
	return len(p), nil
}

func (endlessReader) Seek(int64, int) (int64, error) { return 0, nil }

func TestLoadReadsOnlyAddressableBytes(t *testing.T) {
	var n int
	// A valid 4-byte header followed by an endless run of zero bytes.
	r := struct {
		io.Reader
		io.Seeker
	}{io.MultiReader(bytes.NewReader([]byte{9, 0, 1, 0}), endlessReader{&n}), endlessReader{&n}}
	if _, err := LoadFromReader(r); err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	if n+4 > MaxFontBytes {
		t.Fatalf("read %d bytes, want at most %d", n+4, MaxFontBytes)
	}
}

// layoutFont is a 13-row font with a 10-pixel 'A', a 3-pixel 0xE9 and no
// space glyph.
func layoutFont(t *testing.T) *Font {
	return mustParse(t, buildFont(13, 0, 3, 0, block('A', 10, 13), block(0xE9, 3, 13)))
}

func TestMeasureTextGameRules(t *testing.T) {
	f := layoutFont(t)
	cases := []struct {
		text string
		want int
	}{
		{"A", 10},
		{"A A", 20},     // no glyph for space: advances 0, no padding
		{"AA\nAAA", 20}, // stops at the newline
		{"A\x00A", 10},  // stops at NUL
		{"\xe9", 3},     // raw byte 0xE9 selects glyph 0xE9
		{"é", 0},        // UTF-8 bytes 0xC3 0xA9 have no glyphs
		{EncodeCP1252("é"), 3},
	}
	for _, c := range cases {
		if got := f.MeasureText(c.text); got != c.want {
			t.Errorf("MeasureText(%q) = %d, want %d", c.text, got, c.want)
		}
	}
	if f.Advance(' ') != 0 || f.Advance('A') != 10 {
		t.Errorf("Advance: space %d, A %d", f.Advance(' '), f.Advance('A'))
	}
}

func TestRenderTextGameLayout(t *testing.T) {
	f := layoutFont(t)
	fg := color.RGBA{255, 255, 255, 255}
	bg := color.RGBA{0, 0, 0, 255}
	img := f.RenderText("A A\nA", fg, bg)
	if got := img.Bounds(); got != image.Rect(0, 0, 20, 13) {
		t.Fatalf("bounds = %v, want 20x13", got)
	}
	// The second A starts right after the first: its set column is x = 10.
	for y := 0; y < 13; y++ {
		if img.RGBAAt(0, y) != fg || img.RGBAAt(10, y) != fg || img.RGBAAt(9, y) != bg {
			t.Fatalf("row %d not laid out at x = 0 and x = 10", y)
		}
	}

	// A nil background leaves clear bits transparent.
	img = f.RenderText("A", fg, nil)
	if img.RGBAAt(1, 0).A != 0 || img.RGBAAt(0, 0) != fg {
		t.Error("nil background painted")
	}
}

func TestDrawTextUsesBaseline(t *testing.T) {
	f := layoutFont(t) // baseline 3
	fg := color.RGBA{255, 0, 0, 255}
	dst := image.NewRGBA(image.Rect(0, 0, 40, 40))
	adv := f.DrawText(dst, 5, 20, "A", fg, nil)
	if adv != 10 {
		t.Errorf("advance = %d, want 10", adv)
	}
	if dst.RGBAAt(5, 16).A != 0 || dst.RGBAAt(5, 17) != fg || dst.RGBAAt(5, 29) != fg || dst.RGBAAt(5, 30).A != 0 {
		t.Error("glyph rows are not at y - baseline .. y - baseline + height - 1")
	}
}

func TestEncodeCP1252(t *testing.T) {
	cases := map[string]string{
		"Az":      "Az",
		"é":       "\xe9",
		"€‚™Ÿ":    "\x80\x82\x99\x9f",
		"中":       "?",
		"\xff":    "?",
		"\u0081x": "?x", // a C1 control with no Windows-1252 byte
	}
	for in, want := range cases {
		if got := EncodeCP1252(in); got != want {
			t.Errorf("EncodeCP1252(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRetailFontHeaders checks every retail font against the documented
// header layout: a full table, a small positive baseline and no skipped
// glyphs.
func TestRetailFontHeaders(t *testing.T) {
	dir := testutil.UnpackedDir(t, "fonts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".fnt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		f, err := Parse(data)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		n++
		if f.FirstChar != 0 || f.Baseline < 1 || f.Baseline > 3 || f.Height < 9 || f.Height > 17 {
			t.Errorf("%s: height %d baseline %d first %d", e.Name(), f.Height, f.Baseline, f.FirstChar)
		}
		if len(f.Warnings) != 0 {
			t.Errorf("%s: warnings %v", e.Name(), f.Warnings)
		}
	}
	if n == 0 {
		t.Fatal("no fonts found")
	}
}
