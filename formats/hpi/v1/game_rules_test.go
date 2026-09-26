package v1

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// rawDir assembles a v1 directory block by hand. Positions are absolute file
// offsets; the block starts right after the 20-byte header.
type rawDir struct {
	buf []byte
}

func (d *rawDir) at() uint32 { return uint32(common.HeaderSize + len(d.buf)) }

func (d *rawDir) put(b ...byte) uint32 {
	pos := d.at()
	d.buf = append(d.buf, b...)
	return pos
}

func (d *rawDir) u32(v uint32) uint32 {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return d.put(b[:]...)
}

func (d *rawDir) name(s string) uint32 { return d.put(append([]byte(s), 0)...) }

func (d *rawDir) patch32(abs, v uint32) {
	binary.LittleEndian.PutUint32(d.buf[abs-common.HeaderSize:], v)
}

// node writes a directory node header (count, list offset) and returns the
// offset of the node and of its (reserved) entry list.
func (d *rawDir) node(count int32) (node, list uint32) {
	node = d.u32(uint32(count))
	listField := d.u32(0)
	list = d.at()
	if count > 0 {
		d.buf = append(d.buf, make([]byte, int(count)*common.DirectoryEntrySize)...)
	}
	d.patch32(listField, list)
	return node, list
}

// entry fills entry i of a list.
func (d *rawDir) entry(list uint32, i int, nameOff, dataOff uint32, flags byte) {
	at := list + uint32(i*common.DirectoryEntrySize) - common.HeaderSize
	binary.LittleEndian.PutUint32(d.buf[at:], nameOff)
	binary.LittleEndian.PutUint32(d.buf[at+4:], dataOff)
	d.buf[at+8] = flags
}

// fileRecord writes a file record and returns its offset.
func (d *rawDir) fileRecord(offset, size uint32, comp byte) uint32 {
	pos := d.u32(offset)
	d.u32(size)
	d.put(comp)
	return pos
}

// archive assembles header + directory + data + trailer, encrypting
// everything after the header with the key derived from headerKey.
func archive(headerKey byte, root uint32, dir []byte, data []byte, trailer string) []byte {
	var out bytes.Buffer
	h := common.Header{
		Marker:        common.HeaderMarker,
		Version:       common.VersionV1,
		DirectorySize: uint32(common.HeaderSize + len(dir)),
		DecryptKey:    uint32(headerKey),
		Offset:        root,
	}
	_ = h.WriteHeader(&out)
	body := append(append([]byte(nil), dir...), data...)
	common.EncryptInPlace(common.TransformHeaderKey(headerKey), common.HeaderSize, body)
	out.Write(body)
	out.WriteString(trailer)
	return out.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.hpi")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readEntry(t *testing.T, r *Reader, path string) []byte {
	t.Helper()
	rc, err := r.Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", path, err)
	}
	return b
}

// sqshChunk builds a SQSH chunk.
func sqshChunk(typ byte, payload []byte, unpacked uint32) []byte {
	out := make([]byte, common.SQSHHeaderSize+len(payload))
	binary.LittleEndian.PutUint32(out[0:], common.ChunkMarker)
	out[4] = 2
	out[5] = typ
	binary.LittleEndian.PutUint32(out[7:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[11:], unpacked)
	binary.LittleEndian.PutUint32(out[15:], common.Checksum(payload))
	copy(out[common.SQSHHeaderSize:], payload)
	return out
}

// singleFile builds an archive whose root holds one file with the given
// record fields and data; dataFor receives the data's absolute offset.
func singleFile(headerKey byte, name string, size uint32, comp byte, flags byte, dataFor func(offset uint32) []byte) []byte {
	var d rawDir
	_, list := d.node(1)
	nameOff := d.name(name)
	rec := d.fileRecord(0, size, comp)
	d.entry(list, 0, nameOff, rec, flags)
	dataStart := d.at()
	d.patch32(rec, dataStart)
	return archive(headerKey, common.HeaderSize, d.buf, dataFor(dataStart), common.DefaultTrailer)
}

func TestReaderLocatesChunksThroughSizeTable(t *testing.T) {
	block0 := bytes.Repeat([]byte{'a'}, common.ChunkBlockSize)
	block1 := []byte("tail of the file")
	c0 := sqshChunk(common.CompressionLZ77, common.CompressLZ77(block0), uint32(len(block0)))
	c1 := sqshChunk(common.CompressionLZ77, common.CompressLZ77(block1), uint32(len(block1)))
	pad := []byte{0xEE, 0xEE, 0xEE, 0xEE, 0xEE}
	size := uint32(len(block0) + len(block1))
	data := singleFile(0, "f.bin", size, 1, 0, func(uint32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c0)+len(pad)))
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c1)))
		b.Write(c0)
		b.Write(pad) // padding inside chunk 0's table slot
		b.Write(c1)
		return b.Bytes()
	})
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got := readEntry(t, r, "f.bin")
	if !bytes.Equal(got, append(append([]byte(nil), block0...), block1...)) {
		t.Fatal("chunks were not located through the size table")
	}
}

func TestReaderPlacesChunksInFixedBlocks(t *testing.T) {
	short := []byte("only a hundred bytes or so, not a full 64 KiB block")
	block1 := []byte("second block")
	c0 := sqshChunk(common.CompressionLZ77, common.CompressLZ77(short), uint32(len(short)))
	c1 := sqshChunk(common.CompressionLZ77, common.CompressLZ77(block1), uint32(len(block1)))
	size := uint32(common.ChunkBlockSize + len(block1))
	data := singleFile(0, "f.bin", size, 2, 0, func(uint32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c0)))
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c1)))
		b.Write(c0)
		b.Write(c1)
		return b.Bytes()
	})
	path := writeTemp(t, data)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got := readEntry(t, r, "f.bin")
	want := make([]byte, size)
	copy(want, short)
	copy(want[common.ChunkBlockSize:], block1)
	if !bytes.Equal(got, want) {
		t.Fatal("a short chunk should leave zeros up to the next 64 KiB block")
	}

	strict, err := OpenWithOptions(path, ReadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = strict.Close() }()
	if _, err := strict.Open("f.bin"); err == nil {
		t.Error("strict reader accepted a chunk that does not fill its block")
	}
}

func TestReaderZlibLeniency(t *testing.T) {
	content := bytes.Repeat([]byte("zlib payload "), 10)
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	_, _ = zw.Write(content)
	_ = zw.Close()
	z := zbuf.Bytes()
	z[len(z)-1] ^= 0xFF // break the Adler-32
	c := sqshChunk(common.CompressionZLib, z, uint32(len(content)))
	data := singleFile(common.DefaultHeaderKey, "f.txt", uint32(len(content)), 2, 0, func(uint32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c)))
		b.Write(c)
		return b.Bytes()
	})
	path := writeTemp(t, data)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, "f.txt"); !bytes.Equal(got, content) {
		t.Error("the game keeps a zlib chunk whose Adler-32 fails")
	}
	strict, err := OpenWithOptions(path, ReadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = strict.Close() }()
	if _, err := strict.Open("f.txt"); err == nil {
		t.Error("strict reader accepted a bad Adler-32")
	}
}

func TestReaderRefusesStoredSQSHChunk(t *testing.T) {
	content := []byte("stored inside a chunk")
	c := sqshChunk(common.CompressionNone, content, uint32(len(content)))
	data := singleFile(0, "f.txt", uint32(len(content)), 1, 0, func(uint32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c)))
		b.Write(c)
		return b.Bytes()
	})
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	_, err = r.Open("f.txt")
	if !errors.Is(err, common.ErrChunkSize) {
		t.Fatalf("SQSH type 0 inside a chunked entry: err = %v, want ErrChunkSize", err)
	}
}

func TestReaderHeaderKey0xFFIsPlaintext(t *testing.T) {
	content := []byte("plain bytes")
	data := singleFile(0xFF, "f.txt", uint32(len(content)), 0, 0, func(uint32) []byte { return content })
	// With key 0xFF nothing is encrypted: the name is visible in the file.
	if !bytes.Contains(data, []byte("f.txt\x00")) || !bytes.Contains(data, content) {
		t.Fatal("test archive should be plaintext")
	}
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if r.HeaderKey() != 0xFF || r.EffectiveKey() != 0 {
		t.Errorf("HeaderKey=0x%02X EffectiveKey=0x%02X, want 0xFF/0", r.HeaderKey(), r.EffectiveKey())
	}
	if got := readEntry(t, r, "f.txt"); !bytes.Equal(got, content) {
		t.Errorf("got %q", got)
	}
}

func TestReaderFlagBitZeroAndAnyCompressionByte(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 300)
	c := sqshChunk(common.CompressionLZ77, common.CompressLZ77(content), uint32(len(content)))
	// Flag 0x02 has bit 0 clear, so this is a file; compression byte 7 is
	// non-zero, so it is chunked.
	data := singleFile(0, "f.bin", uint32(len(content)), 7, 0x02, func(uint32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c)))
		b.Write(c)
		return b.Bytes()
	})
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	e := r.Find("f.bin")
	if e == nil || e.IsDir {
		t.Fatalf("flag 0x02 should be a file, got %+v", e)
	}
	if got := readEntry(t, r, "f.bin"); !bytes.Equal(got, content) {
		t.Error("compression byte 7 should read as a chunked entry")
	}
}

func TestReaderNegativeCountIsEmpty(t *testing.T) {
	var d rawDir
	d.u32(0x80000001)
	d.u32(0)
	r, err := Open(writeTemp(t, archive(0, common.HeaderSize, d.buf, nil, common.DefaultTrailer)))
	if err != nil {
		t.Fatalf("negative root count: %v", err)
	}
	defer func() { _ = r.Close() }()
	if n := len(r.List()); n != 0 {
		t.Errorf("List() has %d entries, want 0", n)
	}
}

func TestReaderRootOffset(t *testing.T) {
	var d rawDir
	d.node(0)
	for _, tc := range []struct {
		root uint32
		ok   bool
	}{{0, true}, {common.HeaderSize, true}, {5, false}, {19, false}} {
		r, err := Open(writeTemp(t, archive(0, tc.root, d.buf, nil, common.DefaultTrailer)))
		if (err == nil) != tc.ok {
			t.Errorf("root offset %d: err = %v, want ok=%v", tc.root, err, tc.ok)
		}
		if r != nil {
			_ = r.Close()
		}
	}
}

func TestReaderRejectsCyclesAndSharedNodes(t *testing.T) {
	// Root holds directory "loop" whose node is the root node itself.
	var d rawDir
	rootNode, list := d.node(1)
	d.entry(list, 0, d.name("loop"), rootNode, common.EntryFlagDirectory)
	if _, err := Open(writeTemp(t, archive(0, rootNode, d.buf, nil, common.DefaultTrailer))); err == nil {
		t.Error("a directory cycle was accepted")
	}

	// Root holds two directories that share one node.
	var s rawDir
	sRoot, sList := s.node(2)
	shared, _ := s.node(0)
	s.entry(sList, 0, s.name("a"), shared, common.EntryFlagDirectory)
	s.entry(sList, 1, s.name("b"), shared, common.EntryFlagDirectory)
	if _, err := Open(writeTemp(t, archive(0, sRoot, s.buf, nil, common.DefaultTrailer))); err == nil {
		t.Error("a shared directory node was accepted")
	}
}

func TestReaderLongNames(t *testing.T) {
	long := strings.Repeat("n", 300) + ".txt"
	content := []byte("long")
	data := singleFile(0, long, uint32(len(content)), 0, 0, func(uint32) []byte { return content })
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatalf("300-byte name: %v", err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, long); !bytes.Equal(got, content) {
		t.Errorf("got %q", got)
	}
}

func TestReaderLastDuplicateWins(t *testing.T) {
	var d rawDir
	_, list := d.node(2)
	n0, n1 := d.name("a.txt"), d.name("A.TXT")
	r0 := d.fileRecord(0, 3, 0)
	r1 := d.fileRecord(0, 6, 0)
	d.entry(list, 0, n0, r0, 0)
	d.entry(list, 1, n1, r1, 0)
	start := d.at()
	d.patch32(r0, start)
	d.patch32(r1, start+3)
	r, err := Open(writeTemp(t, archive(0x42, common.HeaderSize, d.buf, []byte("onesecond"), common.DefaultTrailer)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, "a.txt"); string(got) != "second" {
		t.Errorf("a.txt = %q, want the last duplicate's %q", got, "second")
	}
	if e := r.Find("a.txt"); e == nil || e.Size != 6 {
		t.Errorf("Find(a.txt) = %+v, want size 6", e)
	}
	if n := len(r.List()); n != 2 {
		t.Errorf("List() = %d records, want both duplicates listed", n)
	}
}

func TestReaderStoredEntryRawAndTrailer(t *testing.T) {
	content := []byte("stored entry data")
	data := singleFile(common.DefaultHeaderKey, "s.txt", uint32(len(content)), 0, 0, func(uint32) []byte { return content })
	r, err := Open(writeTemp(t, data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	e := r.Find("s.txt")
	raw, err := r.ReadRawFileData(e)
	if err != nil || !bytes.Equal(raw, content) {
		t.Fatalf("ReadRawFileData(stored) = %q, %v", raw, err)
	}
	tr, err := r.ReadTrailer()
	if err != nil || string(tr) != common.DefaultTrailer {
		t.Fatalf("ReadTrailer = %q, %v", tr, err)
	}
	if !r.TrailerValid() {
		t.Error("TrailerValid() = false")
	}
}

func TestReaderEntrySizeCap(t *testing.T) {
	// A stored record claiming 4 GiB fails on the file bounds; a chunked one
	// fails on the entry cap before its output is allocated.
	data := singleFile(0, "big", 0xFFFFFFFF, 1, 0, func(uint32) []byte { return make([]byte, 64) })
	r, err := OpenWithOptions(writeTemp(t, data), ReadOptions{MaxEntrySize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Open("big"); err == nil {
		t.Error("a 4 GiB chunked record was accepted")
	}
	stored := singleFile(0, "big", 0xFFFFFFFF, 0, 0, func(uint32) []byte { return make([]byte, 64) })
	rs, err := Open(writeTemp(t, stored))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	if _, err := rs.Open("big"); err == nil {
		t.Error("a 4 GiB stored record was accepted")
	}
}
