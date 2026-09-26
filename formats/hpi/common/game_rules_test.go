package common

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestTransformHeaderKeyPlaintextBytes(t *testing.T) {
	for _, b := range []uint8{0x00, 0xFF} {
		if got := TransformHeaderKey(b); got != 0 {
			t.Errorf("TransformHeaderKey(0x%02X) = 0x%02X, want 0 (plaintext)", b, got)
		}
		if !IsPlaintextKey(b) {
			t.Errorf("IsPlaintextKey(0x%02X) = false", b)
		}
	}
	if got := TransformHeaderKey(DefaultHeaderKey); got != 0xFE {
		t.Errorf("TransformHeaderKey(0xBF) = 0x%02X, want 0xFE", got)
	}
	if got := TransformHeaderKey(0x7D); got != 0xF5 {
		t.Errorf("TransformHeaderKey(0x7D) = 0x%02X, want 0xF5", got)
	}
}

func TestValidTrailer(t *testing.T) {
	good := []string{
		"Copyright 1997 Cavedog Entertainment",
		"Copyright 2000 Cavedog Entertainment",
		"Copyright ____ Cavedog Entertainment",
		"Copyright \x00\xff?! Cavedog Entertainment",
	}
	for _, s := range good {
		if !ValidTrailer([]byte(s)) {
			t.Errorf("ValidTrailer(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"Copyright 1997 Cavedog Entertainment ",
		"Copyright 1997 Cavedog Entertainmen",
		"copyright 1997 Cavedog Entertainment",
		"Copyright 1997 Cavedog Entertainmenx",
		strings.Repeat("x", TrailerSize),
	}
	for _, s := range bad {
		if ValidTrailer([]byte(s)) {
			t.Errorf("ValidTrailer(%q) = true, want false", s)
		}
	}
	tr, err := Trailer(1998)
	if err != nil || string(tr) != "Copyright 1998 Cavedog Entertainment" {
		t.Errorf("Trailer(1998) = %q, %v", tr, err)
	}
	if _, err := Trailer(12345); err == nil {
		t.Error("Trailer(12345) accepted a five-digit year")
	}
}

// tree builds a root directory holding the given children.
func tree(children ...*Entry) *Entry {
	root := &Entry{IsDir: true}
	adopt(root, children...)
	return root
}

func adopt(parent *Entry, children ...*Entry) *Entry {
	for _, c := range children {
		c.Parent = parent
	}
	parent.Children = append(parent.Children, children...)
	return parent
}

func TestFindTakesLastCaseInsensitiveMatch(t *testing.T) {
	first := &Entry{Name: "a.txt", Size: 3}
	second := &Entry{Name: "A.TXT", Size: 6}
	root := tree(first, second)
	if got := root.Find("a.txt"); got != second {
		t.Fatalf("Find(a.txt) = %+v, want the later A.TXT", got)
	}
	if first.Reachable() || !second.Reachable() {
		t.Errorf("Reachable: first=%v second=%v, want false/true", first.Reachable(), second.Reachable())
	}
}

func TestFindSplitsOnBackslash(t *testing.T) {
	file := &Entry{Name: "ARMCOM.FBI"}
	root := tree(adopt(&Entry{Name: "units", IsDir: true}, file))
	for _, p := range []string{`units\armcom.fbi`, "units/ARMCOM.FBI", `UNITS\ARMCOM.fbi`} {
		if got := root.Find(p); got != file {
			t.Errorf("Find(%q) = %v, want ARMCOM.FBI", p, got)
		}
	}
}

func TestFindEmptySegmentsFail(t *testing.T) {
	file := &Entry{Name: "x.fbi"}
	root := tree(adopt(&Entry{Name: "units", IsDir: true}, file))
	for _, p := range []string{"/units/x.fbi", "units//x.fbi", "units/x.fbi/", `\units\x.fbi`} {
		if got := root.Find(p); got != nil {
			t.Errorf("Find(%q) = %v, want nil", p, got.Name)
		}
	}
	if got := root.Find(""); got != root {
		t.Errorf("Find(\"\") should return the receiver")
	}
}

func TestFindNoFallbackPastFileSegment(t *testing.T) {
	// Directory "units" holding x.fbi, then a root file "UNITS": the last match
	// for "units" is the file, so units/x.fbi is unreachable in this archive.
	x := &Entry{Name: "x.fbi"}
	dir := adopt(&Entry{Name: "units", IsDir: true}, x)
	file := &Entry{Name: "UNITS"}
	root := tree(dir, file)
	if got := root.Find("units/x.fbi"); got != nil {
		t.Fatalf("Find(units/x.fbi) = %v, want nil", got.Name)
	}
	if root.Find("units") != file {
		t.Fatal("Find(units) should return the later file")
	}
	if x.Reachable() {
		t.Error("x.fbi should be unreachable")
	}
	var reached []string
	_ = root.WalkReachable(func(e *Entry) error {
		reached = append(reached, e.FullPath())
		return nil
	})
	if strings.Join(reached, ",") != ",UNITS" {
		t.Errorf("WalkReachable = %q, want [\"\" UNITS]", reached)
	}
}

func TestFindFoldsASCIIOnly(t *testing.T) {
	lower := &Entry{Name: "caf\xe9.tnt"} // CP1252 e-acute
	upper := &Entry{Name: "CAF\xc9.TNT"} // CP1252 E-acute
	root := tree(lower, upper)
	if root.Find("caf\xe9.tnt") != lower {
		t.Error("lower-case accented name should resolve to itself")
	}
	if root.Find("CAF\xc9.tnt") != upper {
		t.Error("upper-case accented name should resolve to itself")
	}
	if !lower.Reachable() || !upper.Reachable() {
		t.Error("names differing in a non-ASCII byte are both reachable")
	}
	if got := ToLowerASCII("MAPS/CAF\xc9.TNT"); got != "maps/caf\xc9.tnt" {
		t.Errorf("ToLowerASCII = %q", got)
	}
	if got := ToUpperASCII("maps/caf\xe9.tnt"); got != "MAPS/CAF\xe9.TNT" {
		t.Errorf("ToUpperASCII = %q", got)
	}
}

func TestFullPathKeepsRawSegments(t *testing.T) {
	file := &Entry{Name: "x.fbi"}
	dotdot := adopt(&Entry{Name: "..", IsDir: true}, file)
	root := tree(adopt(&Entry{Name: "units", IsDir: true}, dotdot))
	if got := file.FullPath(); got != "units/../x.fbi" {
		t.Fatalf("FullPath = %q, want units/../x.fbi", got)
	}
	if root.Find(file.FullPath()) != file {
		t.Error("Find(FullPath()) should resolve the same entry")
	}
}

func TestCleanArchivePath(t *testing.T) {
	good := map[string]string{
		"units/armcom.fbi":     "units/armcom.fbi",
		`units\arm\armcom.fbi`: "units/arm/armcom.fbi",
		"readme.txt":           "readme.txt",
		"...":                  "...",
	}
	for in, want := range good {
		got, err := CleanArchivePath(in)
		if err != nil || got != want {
			t.Errorf("CleanArchivePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "/x", "x/", "a//b", "a/./b", "a/../b", "..", ".", "a\x00b",
		"units/" + strings.Repeat("n", MaxNameLength+1),
	}
	for _, in := range bad {
		if got, err := CleanArchivePath(in); err == nil {
			t.Errorf("CleanArchivePath(%q) = %q, want an error", in, got)
		}
	}
	if _, err := CleanArchivePath(strings.Repeat("n", MaxNameLength)); err != nil {
		t.Errorf("a %d-byte name should be accepted: %v", MaxNameLength, err)
	}
}

func TestDecompressLZ77RequiresTerminatorAndLength(t *testing.T) {
	data := []byte("hello hello hello")
	stream := CompressLZ77(data)
	if _, err := DecompressLZ77(stream, len(data)); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// Drop the terminator: the stream ends before its end marker.
	if _, err := DecompressLZ77(stream[:len(stream)-2], len(data)); err == nil {
		t.Error("stream without terminator was accepted")
	}
	// Stated size too small: the stream decodes past it.
	if _, err := DecompressLZ77(stream, len(data)-1); err == nil {
		t.Error("stream longer than the stated size was accepted")
	}
	// Stated size too large: the stream terminates early.
	if _, err := DecompressLZ77(stream, len(data)+1); err == nil {
		t.Error("stream shorter than the stated size was accepted")
	}
	if _, err := DecompressLZ77(stream, -1); err == nil {
		t.Error("negative size was accepted")
	}
}

// sqsh builds a SQSH chunk around payload (the bytes as they are before the
// optional add/XOR transform).
func sqsh(typ uint8, payload []byte, unpacked uint32, encoded bool) []byte {
	stored := append([]byte(nil), payload...)
	enc := byte(0)
	if encoded {
		EncodeChunkBuffer(stored)
		enc = 1
	}
	out := make([]byte, SQSHHeaderSize+len(stored))
	binary.LittleEndian.PutUint32(out[0:], ChunkMarker)
	out[4] = 2
	out[5] = typ
	out[6] = enc
	binary.LittleEndian.PutUint32(out[7:], uint32(len(stored)))
	binary.LittleEndian.PutUint32(out[11:], unpacked)
	binary.LittleEndian.PutUint32(out[15:], Checksum(stored))
	copy(out[SQSHHeaderSize:], stored)
	return out
}

func zlibBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(data)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeBlockTypes(t *testing.T) {
	data := []byte("some chunk data")
	if out, err := DecodeBlock(sqsh(CompressionLZ77, CompressLZ77(data), uint32(len(data)), true), false); err != nil || !bytes.Equal(out, data) {
		t.Fatalf("LZ77 block: %q, %v", out, err)
	}
	if out, err := DecodeBlock(sqsh(CompressionZLib, zlibBytes(t, data), uint32(len(data)), false), false); err != nil || !bytes.Equal(out, data) {
		t.Fatalf("zlib block: %q, %v", out, err)
	}
	// Stored (type 0) and type 3 chunks are refused inside chunked entries.
	for _, typ := range []uint8{0, 3} {
		if _, err := DecodeBlock(sqsh(typ, data, uint32(len(data)), false), false); !errors.Is(err, ErrChunkSize) {
			t.Errorf("type %d: err = %v, want ErrChunkSize", typ, err)
		}
	}
	for _, typ := range []uint8{4, 7, 255} {
		if _, err := DecodeBlock(sqsh(typ, data, uint32(len(data)), false), false); !errors.Is(err, ErrChunkType) {
			t.Errorf("type %d: err = %v, want ErrChunkType", typ, err)
		}
	}
}

func TestDecodeBlockHeaderChecks(t *testing.T) {
	data := []byte("chunk")
	good := sqsh(CompressionLZ77, CompressLZ77(data), uint32(len(data)), false)

	if _, err := DecodeBlock(good[:10], false); !errors.Is(err, ErrChunkHeader) {
		t.Errorf("short block: %v", err)
	}
	badMarker := append([]byte(nil), good...)
	badMarker[0] ^= 0xFF
	if _, err := DecodeBlock(badMarker, false); !errors.Is(err, ErrChunkHeader) {
		t.Errorf("bad marker: %v", err)
	}
	// The chunk-size table delimits the block: a packed size running past it
	// is refused even when more bytes follow in the file.
	if _, err := DecodeBlock(good[:len(good)-1], false); !errors.Is(err, ErrChunkChecksum) {
		t.Errorf("packed size past block: %v", err)
	}
	badSum := append([]byte(nil), good...)
	badSum[15]++
	if _, err := DecodeBlock(badSum, false); !errors.Is(err, ErrChunkChecksum) {
		t.Errorf("bad checksum: %v", err)
	}
	// The version byte is ignored.
	anyVersion := append([]byte(nil), good...)
	anyVersion[4] = 99
	if _, err := DecodeBlock(anyVersion, false); err != nil {
		t.Errorf("version byte should be ignored: %v", err)
	}
	// Padding after the packed payload, inside the block, is fine.
	if _, err := DecodeBlock(append(append([]byte(nil), good...), 0, 0, 0), false); err != nil {
		t.Errorf("padding after payload: %v", err)
	}
	big := sqsh(CompressionLZ77, CompressLZ77(data), ChunkBlockSize+1, false)
	if _, err := DecodeBlock(big, false); !errors.Is(err, ErrChunkParams) {
		t.Errorf("unpacked > 64 KiB: %v", err)
	}
}

func TestDecodeBlockLZ77Length(t *testing.T) {
	data := []byte("0123456789")
	stream := CompressLZ77(data)
	if _, err := DecodeBlock(sqsh(CompressionLZ77, stream, uint32(len(data))+1, false), false); !errors.Is(err, ErrChunkSize) {
		t.Errorf("LZ77 short of stated size: %v", err)
	}
	if _, err := DecodeBlock(sqsh(CompressionLZ77, stream, uint32(len(data))-1, false), false); !errors.Is(err, ErrChunkSize) {
		t.Errorf("LZ77 past stated size: %v", err)
	}
	if _, err := DecodeBlock(sqsh(CompressionLZ77, stream[:len(stream)-2], uint32(len(data)), false), false); !errors.Is(err, ErrChunkSize) {
		t.Errorf("LZ77 without terminator: %v", err)
	}
}

func TestDecodeBlockZlibLengthContract(t *testing.T) {
	data := bytes.Repeat([]byte("zlib!"), 20)
	z := zlibBytes(t, data)

	// A clean stream shorter than the stated size is refused in both modes.
	for _, strict := range []bool{false, true} {
		if _, err := DecodeBlock(sqsh(CompressionZLib, z, uint32(len(data))+5, false), strict); !errors.Is(err, ErrChunkSize) {
			t.Errorf("strict=%v clean short stream: %v", strict, err)
		}
	}

	// Bad Adler-32: the game keeps the stated length.
	badAdler := append([]byte(nil), z...)
	badAdler[len(badAdler)-1] ^= 0xFF
	out, err := DecodeBlock(sqsh(CompressionZLib, badAdler, uint32(len(data)), false), false)
	if err != nil || !bytes.Equal(out, data) {
		t.Errorf("lenient bad Adler-32: %v", err)
	}
	if _, err := DecodeBlock(sqsh(CompressionZLib, badAdler, uint32(len(data)), false), true); err == nil {
		t.Error("strict mode accepted a bad Adler-32")
	}

	// Over-long stream: the stated size is kept.
	out, err = DecodeBlock(sqsh(CompressionZLib, z, uint32(len(data))-10, false), false)
	if err != nil || !bytes.Equal(out, data[:len(data)-10]) {
		t.Errorf("lenient over-long stream: %q, %v", out, err)
	}
	if _, err := DecodeBlock(sqsh(CompressionZLib, z, uint32(len(data))-10, false), true); err == nil {
		t.Error("strict mode accepted an over-long stream")
	}

	// A stream that is not zlib at all decodes to zeros of the stated size.
	junk := []byte{0x12, 0x34, 0x56, 0x78}
	out, err = DecodeBlock(sqsh(CompressionZLib, junk, 8, false), false)
	if err != nil || !bytes.Equal(out, make([]byte, 8)) {
		t.Errorf("lenient junk stream: %v, %v", out, err)
	}
	if _, err := DecodeBlock(sqsh(CompressionZLib, junk, 8, false), true); err == nil {
		t.Error("strict mode accepted a junk stream")
	}

	// A truncated stream keeps the decoded prefix followed by zeros.
	out, err = DecodeBlock(sqsh(CompressionZLib, z[:len(z)/2], uint32(len(data)), false), false)
	if err != nil || len(out) != len(data) {
		t.Errorf("lenient truncated stream: len=%d err=%v", len(out), err)
	}
}

func TestDecodeChunkRejectsForgedSizes(t *testing.T) {
	data := []byte("tiny")
	// A header claiming 4 GiB from a few bytes of payload must fail before
	// allocating anything of that size.
	lz := sqsh(CompressionLZ77, CompressLZ77(data), 0xFFFFFFFF, false)
	if _, err := DecodeChunk(lz); err == nil {
		t.Error("LZ77 chunk with forged size was accepted")
	}
	z := sqsh(CompressionZLib, zlibBytes(t, data), 0xFFFFFFFF, false)
	if _, err := DecodeChunk(z); err == nil {
		t.Error("zlib chunk with forged size was accepted")
	}
	// Honest chunks still decode, including stored ones (TA: Kingdoms).
	for _, c := range [][]byte{
		sqsh(CompressionLZ77, CompressLZ77(data), uint32(len(data)), true),
		sqsh(CompressionZLib, zlibBytes(t, data), uint32(len(data)), false),
		sqsh(CompressionNone, data, uint32(len(data)), false),
	} {
		if out, err := DecodeChunk(c); err != nil || !bytes.Equal(out, data) {
			t.Errorf("DecodeChunk: %q, %v", out, err)
		}
	}
}

func TestNamesWithSeparatorsAreUnreachable(t *testing.T) {
	odd := &Entry{Name: `a\b`}
	plain := &Entry{Name: "c"}
	root := tree(odd, plain)
	if odd.Reachable() || root.Find(odd.FullPath()) == odd {
		t.Error(`a name containing '\' cannot be reached by a lookup`)
	}
	var seen []string
	_ = root.WalkReachable(func(e *Entry) error {
		if !e.IsDir {
			seen = append(seen, e.Name)
		}
		return nil
	})
	if len(seen) != 1 || seen[0] != "c" {
		t.Errorf("WalkReachable files = %q, want [c]", seen)
	}
}
