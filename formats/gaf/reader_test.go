package gaf

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// gafBytes assembles a GAF image from records appended at increasing
// offsets; pointers are patched once the target offsets are known.
type gafBytes struct{ b []byte }

func (g *gafBytes) add(v any) uint32 {
	off := uint32(len(g.b))
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
		panic(err)
	}
	g.b = append(g.b, buf.Bytes()...)
	return off
}

func (g *gafBytes) set32(at, v uint32) {
	binary.LittleEndian.PutUint32(g.b[at:], v)
}

// frameInfoData is the offset of PtrFrameData inside a FrameInfo.
const frameInfoData = 16

// oneFrameGAF builds a file with one sequence holding one frame.
func oneFrameGAF(seq SequenceHeader, duration uint32, fi FrameInfo, data []byte) []byte {
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	seq.FrameCount = 1
	s := g.add(seq)
	item := g.add(FrameListItem{Duration: duration})
	f := g.add(fi)
	d := g.add(data)
	g.set32(ptr, s)
	g.set32(item, f)
	g.set32(f+frameInfoData, d)
	return g.b
}

func readGAF(t *testing.T, b []byte) ([]*Sequence, *Reader) {
	t.Helper()
	r, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	seqs, err := r.ReadSequences()
	if err != nil {
		t.Fatalf("ReadSequences: %v", err)
	}
	return seqs, r
}

func hasWarning(r *Reader, substr string) bool {
	for _, w := range r.Warnings() {
		if strings.Contains(w.Message, substr) {
			return true
		}
	}
	return false
}

func TestReadSequenceCountUsesSignedLowWord(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 1, Height: 1}, []byte{5})

	// The high word is ignored: 0xABCD0001 is one sequence.
	binary.LittleEndian.PutUint32(b[4:], 0xABCD0001)
	seqs, r := readGAF(t, b)
	if len(seqs) != 1 {
		t.Fatalf("0xABCD0001: got %d sequences, want 1", len(seqs))
	}
	if !hasWarning(r, "low 16 bits") {
		t.Errorf("expected a warning about the high bits, got %v", r.Warnings())
	}

	// Negative low words (and 0xFFFFFFFF, which once asked for a 16 GiB
	// pointer table) mean no sequences.
	for _, count := range []uint32{0xFFFFFFFF, 0x00008000, 0x0000FFFF} {
		binary.LittleEndian.PutUint32(b[4:], count)
		seqs, r := readGAF(t, b)
		if len(seqs) != 0 {
			t.Errorf("count 0x%08X: got %d sequences, want 0", count, len(seqs))
		}
		if !hasWarning(r, "negative") {
			t.Errorf("count 0x%08X: expected a negative-count warning, got %v", count, r.Warnings())
		}
	}

	if got := (Header{SequenceCount: 0x00017FFF}).EffectiveSequenceCount(); got != MaxSequences {
		t.Errorf("EffectiveSequenceCount(0x00017FFF) = %d, want %d", got, MaxSequences)
	}
}

func TestReadKeepsSequenceWords(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{Unknown1: 0x0101, Unknown2: 0xDEADBEEF}, 1, FrameInfo{Width: 1, Height: 1}, []byte{5})
	seqs, _ := readGAF(t, b)
	s := seqs[0]
	if s.LoopFlags != 0x0101 || s.Unknown4 != 0xDEADBEEF {
		t.Errorf("LoopFlags=0x%04X Unknown4=0x%08X, want 0x0101 and 0xDEADBEEF", s.LoopFlags, s.Unknown4)
	}
	if !s.Loops() {
		t.Error("Loops() = false for a low byte of 1")
	}
	s.LoopFlags = 0x0100
	if s.Loops() {
		t.Error("Loops() = true when only the high byte is set; the game tests the low byte")
	}
	s.SetLoops(true)
	if s.LoopFlags != 0x0101 {
		t.Errorf("SetLoops(true) gave 0x%04X, want 0x0101", s.LoopFlags)
	}
	s.SetLoops(false)
	if s.LoopFlags != 0x0100 {
		t.Errorf("SetLoops(false) gave 0x%04X, want 0x0100", s.LoopFlags)
	}
}

func TestReadKeepsFrameStorageAndWords(t *testing.T) {
	raw := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 2, Height: 1, Unknown2: 7, Unknown3: 480}, []byte{1, 2})
	seqs, _ := readGAF(t, raw)
	f := seqs[0].Frames[0]
	if f.Storage != StorageRaw {
		t.Errorf("raw frame Storage = %v", f.Storage)
	}
	if f.Unknown12 != 7 || f.Unknown20 != 480 {
		t.Errorf("Unknown12=%d Unknown20=%d, want 7 and 480", f.Unknown12, f.Unknown20)
	}

	comp := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 2, Height: 1, Compressed: 1}, []byte{2, 0, 0x06, 4})
	seqs, _ = readGAF(t, comp)
	f = seqs[0].Frames[0]
	if f.Storage != StorageCompressed || !bytes.Equal(f.Pixels, []byte{4, 4}) {
		t.Errorf("compressed frame: Storage=%v Pixels=%v, want compressed [4 4]", f.Storage, f.Pixels)
	}
}

// Byte +11 is a flag of its own: {+10=0, +11=1} is a simple frame, not 256
// layers.
func TestReadBlendByteIsSeparateFromLayerCount(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 2, Height: 1, Blend: 1}, []byte{3, 4})
	seqs, _ := readGAF(t, b)
	f := seqs[0].Frames[0]
	if f.Blend != 1 || len(f.Layers) != 0 || !bytes.Equal(f.Pixels, []byte{3, 4}) {
		t.Errorf("Blend=%d Layers=%d Pixels=%v, want a simple frame [3 4] with Blend 1", f.Blend, len(f.Layers), f.Pixels)
	}
}

func TestReadDurationUsesLow16Bits(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{}, 0xBEEF0007, FrameInfo{Width: 1, Height: 1}, []byte{5})
	seqs, r := readGAF(t, b)
	if got := seqs[0].Frames[0].Duration; got != 7 {
		t.Errorf("Duration = %d, want 7", got)
	}
	if !hasWarning(r, "duration word") {
		t.Errorf("expected a duration warning, got %v", r.Warnings())
	}
}

func TestReadAcceptsAnyVersion(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 1, Height: 1}, []byte{5})
	binary.LittleEndian.PutUint32(b, 0x00010000)
	seqs, r := readGAF(t, b)
	if len(seqs) != 1 || r.Header().Version != 0x00010000 {
		t.Fatalf("got %d sequences, version 0x%08X", len(seqs), r.Header().Version)
	}
	if !hasWarning(r, "version") {
		t.Errorf("expected a version warning, got %v", r.Warnings())
	}

	binary.LittleEndian.PutUint32(b, 0)
	_, r = readGAF(t, b)
	if len(r.Warnings()) != 0 {
		t.Errorf("version 0 (used by stock files) should not warn: %v", r.Warnings())
	}
}

// A compressed frame's repeat and literal commands draw even when their
// value equals the transparency index; only skips are transparent.
func TestReadCompressedKeyValuedPixelsStayOpaque(t *testing.T) {
	// Width 3, key 9: literal {9}, skip 1, repeat 9 (clipped to 1 pixel).
	row := []byte{5, 0, 0x00, 9, 0x03, 0x06, 9}
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1}, row)
	seqs, _ := readGAF(t, b)
	f := seqs[0].Frames[0]
	if !bytes.Equal(f.Pixels, []byte{9, 9, 9}) {
		t.Fatalf("Pixels = %v, want [9 9 9]", f.Pixels)
	}
	want := []bool{true, false, true}
	for i, w := range want {
		if f.PixelOpaque(i) != w {
			t.Errorf("PixelOpaque(%d) = %v, want %v", i, f.PixelOpaque(i), w)
		}
	}

	// Without key-valued pixels no mask is kept.
	plain := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 2, Height: 1, TransparencyIndex: 9, Compressed: 1}, []byte{3, 0, 0x03, 0x00, 4})
	seqs, _ = readGAF(t, plain)
	if f := seqs[0].Frames[0]; f.Opaque != nil || !bytes.Equal(f.Pixels, []byte{9, 4}) {
		t.Errorf("Pixels=%v Opaque=%v, want [9 4] and no mask", f.Pixels, f.Opaque)
	}
}

// compositeGAF builds one composite frame of the given size whose layers are
// the given frame headers, each followed by its data.
func compositeGAF(parent FrameInfo, layers []FrameInfo, data [][]byte) []byte {
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	s := g.add(SequenceHeader{FrameCount: 1, Unknown1: 1})
	item := g.add(FrameListItem{Duration: 2})
	parent.LayerCount = uint8(len(layers))
	p := g.add(parent)
	table := g.add(make([]uint32, len(layers)))
	for i, l := range layers {
		lo := g.add(l)
		d := g.add(data[i])
		g.set32(lo+frameInfoData, d)
		g.set32(table+uint32(4*i), lo)
	}
	g.set32(ptr, s)
	g.set32(item, p)
	g.set32(p+frameInfoData, table)
	return g.b
}

func TestReadCompositeKeepsLayersAndCoverage(t *testing.T) {
	bottom := FrameInfo{Width: 3, Height: 1}
	// Key-valued literal and repeat over a layer of 7s: {9,7,9}.
	top := FrameInfo{Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1, Blend: 1}
	b := compositeGAF(FrameInfo{Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1},
		[]FrameInfo{bottom, top}, [][]byte{{7, 7, 7}, {5, 0, 0x00, 9, 0x03, 0x06, 9}})
	seqs, r := readGAF(t, b)
	f := seqs[0].Frames[0]
	if len(f.Layers) != 2 {
		t.Fatalf("got %d layers, want 2", len(f.Layers))
	}
	if f.Layers[1].Blend != 1 {
		t.Error("the flagged layer lost its +11 byte")
	}
	if f.Duration != 2 || f.Layers[0].Duration != 0 {
		t.Errorf("durations: frame %d, layer %d; want 2 and 0", f.Duration, f.Layers[0].Duration)
	}
	if !bytes.Equal(f.Pixels, []byte{9, 7, 9}) {
		t.Fatalf("Pixels = %v, want [9 7 9]", f.Pixels)
	}
	for i := 0; i < 3; i++ {
		if !f.PixelOpaque(i) {
			t.Errorf("pixel %d should be opaque", i)
		}
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", r.Warnings())
	}
}

func TestReadReportsNestedAndSelfReferencingLayers(t *testing.T) {
	// Layer 0 is a composite of its own; layer 1 points back at the parent.
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	s := g.add(SequenceHeader{FrameCount: 1})
	item := g.add(FrameListItem{Duration: 1})
	p := g.add(FrameInfo{Width: 1, Height: 1, LayerCount: 3})
	table := g.add([]uint32{0, 0, 0})
	nested := g.add(FrameInfo{Width: 1, Height: 1, LayerCount: 1})
	nestedTable := g.add([]uint32{0})
	leaf := g.add(FrameInfo{Width: 1, Height: 1})
	leafData := g.add([]byte{6})
	g.set32(ptr, s)
	g.set32(item, p)
	g.set32(p+frameInfoData, table)
	g.set32(table, nested)
	g.set32(table+4, p)
	g.set32(table+8, leaf)
	g.set32(nested+frameInfoData, nestedTable)
	g.set32(nestedTable, leaf)
	g.set32(leaf+frameInfoData, leafData)

	seqs, r := readGAF(t, g.b)
	f := seqs[0].Frames[0]
	if len(f.Layers) != 1 || !bytes.Equal(f.Pixels, []byte{6}) {
		t.Errorf("Layers=%d Pixels=%v, want the one simple layer drawn", len(f.Layers), f.Pixels)
	}
	if !hasWarning(r, "itself a composite") {
		t.Errorf("nested layer not reported: %v", r.Warnings())
	}
	if !hasWarning(r, "refers to its own frame") {
		t.Errorf("self-reference not reported: %v", r.Warnings())
	}
}

// A composite whose only layer was skipped has no Layers once decoded; used
// later as a layer of another composite it is still a nested composite.
func TestReadReportsDecodedCompositeUsedAsLayer(t *testing.T) {
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	s := g.add(SequenceHeader{FrameCount: 2})
	items := g.add([]FrameListItem{{Duration: 1}, {Duration: 1}})
	a := g.add(FrameInfo{Width: 1, Height: 1, TransparencyIndex: 3, LayerCount: 1})
	aTable := g.add([]uint32{0})
	b := g.add(FrameInfo{Width: 1, Height: 1, TransparencyIndex: 3, LayerCount: 1})
	bTable := g.add([]uint32{0})
	g.set32(ptr, s)
	g.set32(items, a)
	g.set32(items+8, b)
	g.set32(a+frameInfoData, aTable)
	g.set32(aTable, a) // a's layer is a itself
	g.set32(b+frameInfoData, bTable)
	g.set32(bTable, a) // b's layer is the composite a

	seqs, r := readGAF(t, g.b)
	if f := seqs[0].Frames[0]; len(f.Layers) != 0 {
		t.Errorf("frame a kept %d layers, want 0", len(f.Layers))
	}
	f := seqs[0].Frames[1]
	if len(f.Layers) != 0 || !bytes.Equal(f.Pixels, []byte{3}) || f.PixelOpaque(0) {
		t.Errorf("frame b: Layers=%d Pixels=%v, want the composite layer skipped", len(f.Layers), f.Pixels)
	}
	if !hasWarning(r, "itself a composite") {
		t.Errorf("nested composite layer not reported: %v", r.Warnings())
	}
}

// Layers are placed by hotspot and clipped to the frame on every side;
// layers with no width or wholly outside the frame draw nothing.
func TestReadCompositeClipsLayersByHotspot(t *testing.T) {
	big := FrameInfo{Width: 4, Height: 3, OriginX: 2, OriginY: 2}
	empty := FrameInfo{Width: 0, Height: 3}
	far := FrameInfo{Width: 1, Height: 1, OriginX: -5}
	corner := FrameInfo{Width: 1, Height: 1}
	b := compositeGAF(FrameInfo{Width: 2, Height: 2, OriginX: 1, OriginY: 1},
		[]FrameInfo{big, empty, far, corner},
		[][]byte{{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, {}, {50}, {99}})
	seqs, r := readGAF(t, b)
	f := seqs[0].Frames[0]
	if len(f.Layers) != 4 {
		t.Fatalf("got %d layers, want 4", len(f.Layers))
	}
	if !bytes.Equal(f.Pixels, []byte{6, 7, 10, 99}) {
		t.Errorf("Pixels = %v, want [6 7 10 99]", f.Pixels)
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", r.Warnings())
	}
}

// Many frame headers pointing at one blob of long compressed rows produce
// little pixel data but make the reader scan the blob once per header; the
// row bytes count towards the decode limit so the read stops early.
func TestReadRowBytesCountTowardsDecodeLimit(t *testing.T) {
	const headers, rows = 200, 100
	row := make([]byte, 2+0xFFFF)
	binary.LittleEndian.PutUint16(row, 0xFFFF)
	for i := 2; i < len(row); i++ {
		row[i] = 0x01 // skip zero pixels
	}
	if headers*rows*len(row) <= maxDecodedBytes || headers*rows > 1<<20 {
		t.Fatal("the rows must exceed the limit while the pixels stay far below it")
	}

	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	s := g.add(SequenceHeader{FrameCount: headers})
	items := g.add(make([]FrameListItem, headers))
	infos := make([]FrameInfo, headers)
	for i := range infos {
		infos[i] = FrameInfo{Width: 1, Height: rows, Compressed: 1}
	}
	first := g.add(infos)
	blob := g.add(bytes.Repeat(row, rows))
	g.set32(ptr, s)
	for i := uint32(0); i < headers; i++ {
		f := first + 24*i
		g.set32(f+frameInfoData, blob)
		g.set32(items+8*i, f)
	}

	r, err := LoadFromReader(bytes.NewReader(g.b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadSequences(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected the decode limit to stop the read, got %v", err)
	}
}

func TestReadReportsSharedFrameHeaders(t *testing.T) {
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	s := g.add(SequenceHeader{FrameCount: 2})
	items := g.add([]FrameListItem{{Duration: 1}, {Duration: 4}})
	f := g.add(FrameInfo{Width: 2, Height: 1})
	d := g.add([]byte{1, 2})
	g.set32(ptr, s)
	g.set32(items, f)
	g.set32(items+8, f)
	g.set32(f+frameInfoData, d)

	seqs, r := readGAF(t, g.b)
	fr := seqs[0].Frames
	if fr[0] == fr[1] || fr[0].Duration != 1 || fr[1].Duration != 4 || !bytes.Equal(fr[1].Pixels, []byte{1, 2}) {
		t.Fatalf("shared header should give two independent frames: %+v %+v", fr[0], fr[1])
	}
	fr[0].Pixels[0] = 99
	if fr[1].Pixels[0] != 1 {
		t.Error("frames decoded from one header share their pixel buffer")
	}
	if !hasWarning(r, "referenced more than once") {
		t.Errorf("shared header not reported: %v", r.Warnings())
	}
}

// Frames with no pixels read no data, whatever their data offset says.
func TestReadEmptyFrameIgnoresDataOffset(t *testing.T) {
	for _, fi := range []FrameInfo{
		{Width: 0, Height: 5, Compressed: 1},
		{Width: 5, Height: 0},
	} {
		b := oneFrameGAF(SequenceHeader{}, 1, fi, nil)
		binary.LittleEndian.PutUint32(b[len(b)-24+frameInfoData:], 0x7FFFFFFF)
		seqs, _ := readGAF(t, b)
		if f := seqs[0].Frames[0]; len(f.Pixels) != 0 {
			t.Errorf("%dx%d frame has %d pixels", fi.Width, fi.Height, len(f.Pixels))
		}
	}
}

func TestReadShortCompressedRowIsPaddedAndReported(t *testing.T) {
	// Width 4 but the row only covers two pixels, then a repeat lacks its
	// value.
	row := []byte{3, 0, 0x04, 1, 2}
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 4, Height: 1, TransparencyIndex: 9, Compressed: 1}, row)
	seqs, r := readGAF(t, b)
	if f := seqs[0].Frames[0]; !bytes.Equal(f.Pixels, []byte{1, 2, 9, 9}) {
		t.Errorf("Pixels = %v, want [1 2 9 9]", f.Pixels)
	}
	if !hasWarning(r, "end before the frame width") {
		t.Errorf("short row not reported: %v", r.Warnings())
	}

	missing := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 2, Height: 1, TransparencyIndex: 9, Compressed: 1}, []byte{1, 0, 0x06})
	seqs, r = readGAF(t, missing)
	if f := seqs[0].Frames[0]; !bytes.Equal(f.Pixels, []byte{9, 9}) {
		t.Errorf("Pixels = %v, want [9 9]", f.Pixels)
	}
	if !hasWarning(r, "end before the frame width") {
		t.Errorf("row with a missing repeat value not reported: %v", r.Warnings())
	}
}

// One large frame header referenced from many slots must not be decoded
// into unbounded memory.
func TestReadDecodeBudget(t *testing.T) {
	g := &gafBytes{}
	g.add(Header{Version: VersionTA, SequenceCount: 1})
	ptr := g.add(uint32(0))
	const slots = 1000
	s := g.add(SequenceHeader{FrameCount: slots})
	items := g.add(make([]FrameListItem, slots))
	f := g.add(FrameInfo{Width: 100, Height: 100, Compressed: 1})
	d := g.add(make([]byte, 2*100)) // 100 empty rows
	g.set32(ptr, s)
	for i := 0; i < slots; i++ {
		g.set32(items+uint32(8*i), f)
	}
	g.set32(f+frameInfoData, d)

	r, err := LoadFromReader(bytes.NewReader(g.b))
	if err != nil {
		t.Fatal(err)
	}
	dec := newDecoder(r.file)
	dec.budget = 500 * 100 * 100
	if _, err := dec.readSequences(r.header); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected the decode budget to stop the read, got %v", err)
	}

	// The same file fits the real budget.
	if _, err := r.ReadSequences(); err != nil {
		t.Fatalf("ReadSequences: %v", err)
	}
}

func TestReadFrameRecordLimit(t *testing.T) {
	b := oneFrameGAF(SequenceHeader{}, 1, FrameInfo{Width: 1, Height: 1}, []byte{5})
	r, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	dec := newDecoder(r.file)
	dec.records = maxFrameRecords
	if _, err := dec.readSequences(r.header); err == nil || !strings.Contains(err.Error(), "frame headers") {
		t.Fatalf("expected the frame-record limit to stop the read, got %v", err)
	}
}
