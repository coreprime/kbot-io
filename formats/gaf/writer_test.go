package gaf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func TestWriteReadRoundtrip(t *testing.T) {
	// Create a simple test sequence
	seq := &Sequence{
		Name: "TestSeq",
		Frames: []*Frame{
			{
				Width: 4, Height: 3,
				OriginX: 0, OriginY: 0,
				TransparencyIndex: 9,
				Duration:          10,
				Pixels: []byte{
					9, 9, 1, 2,
					3, 3, 3, 9,
					5, 6, 9, 9,
				},
			},
		},
	}

	// Write
	var buf bytes.Buffer
	if err := WriteGAF(&buf, []*Sequence{seq}); err != nil {
		t.Fatalf("WriteGAF: %v", err)
	}
	t.Logf("Wrote %d bytes", buf.Len())

	// Read back
	reader, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}

	seqs, err := reader.ReadSequences()
	if err != nil {
		t.Fatalf("ReadSequences: %v", err)
	}

	if len(seqs) != 1 {
		t.Fatalf("expected 1 sequence, got %d", len(seqs))
	}
	if seqs[0].Name != "TestSeq" {
		t.Errorf("name = %q, want TestSeq", seqs[0].Name)
	}
	if len(seqs[0].Frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(seqs[0].Frames))
	}

	f := seqs[0].Frames[0]
	if f.Width != 4 || f.Height != 3 {
		t.Errorf("dims = %dx%d, want 4x3", f.Width, f.Height)
	}

	orig := seq.Frames[0].Pixels
	if len(f.Pixels) != len(orig) {
		t.Fatalf("pixel count = %d, want %d", len(f.Pixels), len(orig))
	}
	for i := range orig {
		if f.Pixels[i] != orig[i] {
			t.Errorf("pixel[%d] = %d, want %d", i, f.Pixels[i], orig[i])
		}
	}
}

func writeRead(t *testing.T, seqs []*Sequence, opts WriteOptions) ([]byte, []*Sequence) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteGAFWith(&buf, seqs, opts); err != nil {
		t.Fatalf("WriteGAFWith: %v", err)
	}
	out, _ := readGAF(t, buf.Bytes())
	return buf.Bytes(), out
}

// frameHeaderAt returns the frame header of frame fi of sequence si.
func frameHeaderAt(t *testing.T, b []byte, si, fi int) FrameInfo {
	t.Helper()
	seq := binary.LittleEndian.Uint32(b[12+4*si:])
	at := binary.LittleEndian.Uint32(b[seq+40+uint32(8*fi):])
	var info FrameInfo
	if err := binary.Read(bytes.NewReader(b[at:]), binary.LittleEndian, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestWriteKeepsStorage(t *testing.T) {
	pixels := []byte{9, 1, 1, 1, 9, 2}
	seq := &Sequence{Name: "s", Frames: []*Frame{
		{Width: 3, Height: 2, TransparencyIndex: 9, Pixels: pixels, Storage: StorageRaw},
		{Width: 3, Height: 2, TransparencyIndex: 9, Pixels: pixels, Storage: StorageCompressed},
		{Width: 3, Height: 2, TransparencyIndex: 9, Pixels: pixels},
	}}

	b, out := writeRead(t, []*Sequence{seq}, WriteOptions{})
	want := []FrameStorage{StorageRaw, StorageCompressed, StorageCompressed}
	for i, f := range out[0].Frames {
		if f.Storage != want[i] {
			t.Errorf("frame %d: Storage = %v, want %v", i, f.Storage, want[i])
		}
		if !bytes.Equal(f.Pixels, pixels) {
			t.Errorf("frame %d: Pixels = %v", i, f.Pixels)
		}
	}
	// The raw frame's data is the pixels themselves.
	info := frameHeaderAt(t, b, 0, 0)
	if info.Compressed != 0 || !bytes.Equal(b[info.PtrFrameData:info.PtrFrameData+6], pixels) {
		t.Errorf("raw frame not stored as plain pixels: %+v", info)
	}

	// DefaultStorage decides for frames that do not say.
	_, out = writeRead(t, []*Sequence{seq}, WriteOptions{DefaultStorage: StorageRaw})
	want = []FrameStorage{StorageRaw, StorageCompressed, StorageRaw}
	for i, f := range out[0].Frames {
		if f.Storage != want[i] {
			t.Errorf("DefaultStorage raw, frame %d: Storage = %v, want %v", i, f.Storage, want[i])
		}
	}
}

func TestStorageForPath(t *testing.T) {
	for path, want := range map[string]FrameStorage{
		"textures/ARMVEHIC.GAF":     StorageRaw,
		`C:\mods\Textures\x.gaf`:    StorageRaw,
		"anims/vismasks.gaf":        StorageRaw,
		"ANIMS/VISMASK.GAF":         StorageRaw,
		"anims/armacv1.gaf":         StorageDefault,
		"anims/texturesample.gaf":   StorageDefault,
		"fx.gaf":                    StorageDefault,
		"anims/buildpic/foo.gaf":    StorageDefault,
		"mytextures/notreally.gaf":  StorageDefault,
		"textures":                  StorageDefault,
		"":                          StorageDefault,
		"anims/vismasks_backup.gaf": StorageRaw,
	} {
		if got := StorageForPath(path); got != want {
			t.Errorf("StorageForPath(%q) = %v, want %v", path, got, want)
		}
	}
	for s, want := range map[string]FrameStorage{"raw": StorageRaw, " Compressed ": StorageCompressed, "": StorageDefault, "default": StorageDefault} {
		got, err := ParseFrameStorage(s)
		if err != nil || got != want {
			t.Errorf("ParseFrameStorage(%q) = %v, %v", s, got, err)
		}
		if want != StorageDefault {
			if back, _ := ParseFrameStorage(got.String()); back != got {
				t.Errorf("String/Parse round trip of %v gave %v", got, back)
			}
		}
	}
	if _, err := ParseFrameStorage("rle"); err == nil {
		t.Error("ParseFrameStorage accepted an unknown name")
	}
}

func TestWriteKeepsSequenceWordsAndFrameBytes(t *testing.T) {
	seq := &Sequence{Name: "loop", LoopFlags: 0x0101, Unknown4: 0xCAFEF00D, Frames: []*Frame{
		{Width: 1, Height: 1, Pixels: []byte{4}, Blend: 1, Unknown12: 7, Unknown20: 480, Duration: MaxDuration},
	}}
	b, out := writeRead(t, []*Sequence{seq}, WriteOptions{})
	s := out[0]
	if s.LoopFlags != 0x0101 || s.Unknown4 != 0xCAFEF00D || !s.Loops() {
		t.Errorf("sequence words: LoopFlags=0x%04X Unknown4=0x%08X", s.LoopFlags, s.Unknown4)
	}
	f := s.Frames[0]
	if f.Blend != 1 || f.Unknown12 != 7 || f.Unknown20 != 480 || f.Duration != MaxDuration {
		t.Errorf("frame fields: Blend=%d Unknown12=%d Unknown20=%d Duration=%d", f.Blend, f.Unknown12, f.Unknown20, f.Duration)
	}
	info := frameHeaderAt(t, b, 0, 0)
	if info.LayerCount != 0 || info.Blend != 1 {
		t.Errorf("bytes +10/+11 = %d/%d, want 0/1", info.LayerCount, info.Blend)
	}
	seqAt := binary.LittleEndian.Uint32(b[12:])
	if got := binary.LittleEndian.Uint16(b[seqAt+2:]); got != 0x0101 {
		t.Errorf("sequence word +2 = 0x%04X, want 0x0101", got)
	}
}

// Key-valued pixels that a compressed frame draws survive a rewrite.
func TestWriteCompressedKeepsOpaqueKeyPixels(t *testing.T) {
	frame := &Frame{Width: 3, Height: 1, TransparencyIndex: 9, Pixels: []byte{9, 9, 9}, Opaque: []bool{true, false, true}}
	_, out := writeRead(t, []*Sequence{{Name: "k", Frames: []*Frame{frame}}}, WriteOptions{})
	f := out[0].Frames[0]
	for i, want := range frame.Opaque {
		if f.PixelOpaque(i) != want {
			t.Errorf("PixelOpaque(%d) = %v, want %v", i, f.PixelOpaque(i), want)
		}
	}
}

func TestWriteKeepsLayers(t *testing.T) {
	b := compositeGAF(FrameInfo{Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1},
		[]FrameInfo{{Width: 3, Height: 1, OriginX: 1}, {Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1, Blend: 1}},
		[][]byte{{7, 7, 7}, {5, 0, 0x00, 9, 0x03, 0x06, 9}})
	in, _ := readGAF(t, b)

	written, out := writeRead(t, in, WriteOptions{})
	f := out[0].Frames[0]
	if len(f.Layers) != 2 || f.Layers[1].Blend != 1 || f.Layers[0].OriginX != 1 || f.Layers[0].Storage != StorageRaw {
		t.Fatalf("layers not kept: %+v", f.Layers)
	}
	if !bytes.Equal(f.Pixels, in[0].Frames[0].Pixels) || f.Duration != 2 || !out[0].Loops() {
		t.Errorf("composite changed: Pixels=%v Duration=%d", f.Pixels, f.Duration)
	}
	if info := frameHeaderAt(t, written, 0, 0); info.LayerCount != 2 {
		t.Errorf("byte +10 = %d, want 2", info.LayerCount)
	}

	// FlattenLayers writes the flattened pixels as one simple frame.
	_, flat := writeRead(t, in, WriteOptions{FlattenLayers: true})
	g := flat[0].Frames[0]
	if len(g.Layers) != 0 || !bytes.Equal(g.Pixels, f.Pixels) {
		t.Fatalf("flattened frame: Layers=%d Pixels=%v", len(g.Layers), g.Pixels)
	}
	for i := range g.Pixels {
		if g.PixelOpaque(i) != f.PixelOpaque(i) {
			t.Errorf("flattened PixelOpaque(%d) = %v, want %v", i, g.PixelOpaque(i), f.PixelOpaque(i))
		}
	}
}

func TestWriteRejectsWhatCannotBeStored(t *testing.T) {
	ok := func() *Frame { return &Frame{Width: 2, Height: 1, Pixels: []byte{1, 2}} }
	many := func(n int) []*Frame {
		fs := make([]*Frame, n)
		for i := range fs {
			fs[i] = ok()
		}
		return fs
	}
	wide := &Frame{Width: 65535, Height: 1, Pixels: make([]byte, 65535)}
	for i := range wide.Pixels {
		wide.Pixels[i] = byte(i%200) + 1 // no runs: every pixel is a literal
	}
	nested := ok()
	nested.Layers = []*Frame{ok()}

	cases := map[string][]*Sequence{
		"short pixels":   {{Frames: []*Frame{{Width: 4, Height: 4, Pixels: []byte{1}}}}},
		"opaque length":  {{Frames: []*Frame{{Width: 2, Height: 1, Pixels: []byte{1, 2}, Opaque: []bool{true}}}}},
		"long name":      {{Name: strings.Repeat("n", MaxNameLength+1), Frames: many(1)}},
		"duration":       {{Frames: []*Frame{{Width: 1, Height: 1, Pixels: []byte{1}, Duration: MaxDuration + 1}}}},
		"too many":       {{Frames: many(MaxFramesPerSequence + 1)}},
		"nil sequence":   {nil},
		"nil frame":      {{Frames: []*Frame{nil}}},
		"long row":       {{Frames: []*Frame{wide}}},
		"nested layers":  {{Frames: []*Frame{{Width: 2, Height: 1, Layers: []*Frame{nested}}}}},
		"too many layer": {{Frames: []*Frame{{Width: 2, Height: 1, Layers: many(MaxLayers + 1)}}}},
		"nil layer":      {{Frames: []*Frame{{Width: 2, Height: 1, Layers: []*Frame{nil}}}}},
	}
	for name, seqs := range cases {
		var buf bytes.Buffer
		if err := WriteGAF(&buf, seqs); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if buf.Len() != 0 {
			t.Errorf("%s: %d bytes written despite the error", name, buf.Len())
		}
	}

	seqs := make([]*Sequence, MaxSequences+1)
	if err := WriteGAF(io.Discard, seqs); err == nil {
		t.Error("more than MaxSequences sequences accepted")
	}

	// The limits themselves are fine.
	if err := WriteGAF(io.Discard, []*Sequence{{Name: strings.Repeat("n", MaxNameLength), Frames: many(1)}}); err != nil {
		t.Errorf("a %d-byte name: %v", MaxNameLength, err)
	}
	if err := WriteGAF(io.Discard, []*Sequence{{Frames: []*Frame{{Width: 2, Height: 1, Layers: many(MaxLayers)}}}}); err != nil {
		t.Errorf("%d layers: %v", MaxLayers, err)
	}
	if err := WriteGAFWith(io.Discard, []*Sequence{{Frames: []*Frame{wide}}}, WriteOptions{DefaultStorage: StorageRaw}); err != nil {
		t.Errorf("a 65535-pixel raw row: %v", err)
	}
}

// Stock files (composites, raw textures, raw sight masks) survive read, write
// and read with every kept field intact.
func TestWriteRoundTripsStockFiles(t *testing.T) {
	for _, rel := range [][]string{{"anims", "commongui.gaf"}, {"textures", "armvehic.gaf"}, {"anims", "vismasks.gaf"}} {
		path := testutil.UnpackedFile(t, rel...)
		r, err := LoadFromFile(path)
		if err != nil {
			t.Fatal(err)
		}
		in, err := r.ReadSequences()
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		_, out := writeRead(t, in, WriteOptions{})
		if len(out) != len(in) {
			t.Fatalf("%s: %d sequences, want %d", path, len(out), len(in))
		}
		for si := range in {
			a, b := in[si], out[si]
			if a.Name != b.Name || a.LoopFlags != b.LoopFlags || a.Unknown4 != b.Unknown4 || len(a.Frames) != len(b.Frames) {
				t.Fatalf("%s sequence %d differs", path, si)
			}
			for fi := range a.Frames {
				if msg := frameDiff(a.Frames[fi], b.Frames[fi]); msg != "" {
					t.Fatalf("%s sequence %s frame %d: %s", path, a.Name, fi, msg)
				}
			}
		}
	}
}

func frameDiff(a, b *Frame) string {
	switch {
	case a.Width != b.Width || a.Height != b.Height || a.OriginX != b.OriginX || a.OriginY != b.OriginY:
		return "geometry"
	case a.TransparencyIndex != b.TransparencyIndex || a.Duration != b.Duration:
		return "key or duration"
	case a.Storage != b.Storage || a.Blend != b.Blend || a.Unknown12 != b.Unknown12 || a.Unknown20 != b.Unknown20:
		return fmt.Sprintf("storage/flags: %v/%d/%d/%d vs %v/%d/%d/%d", a.Storage, a.Blend, a.Unknown12, a.Unknown20, b.Storage, b.Blend, b.Unknown12, b.Unknown20)
	case !bytes.Equal(a.Pixels, b.Pixels):
		return "pixels"
	case len(a.Layers) != len(b.Layers):
		return "layer count"
	}
	for i := range a.Pixels {
		if a.PixelOpaque(i) != b.PixelOpaque(i) {
			return fmt.Sprintf("opacity of pixel %d", i)
		}
	}
	for i := range a.Layers {
		if msg := frameDiff(a.Layers[i], b.Layers[i]); msg != "" {
			return fmt.Sprintf("layer %d: %s", i, msg)
		}
	}
	return ""
}
