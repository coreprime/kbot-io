package v1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

type fileSpec struct {
	path string
	data []byte
}

// writeArchiveFiles writes files with a writer configured by setup and
// returns the archive path.
func writeArchiveFiles(t *testing.T, files []fileSpec, setup func(*Writer)) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out.hpi")
	w, err := CreateWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(w)
	}
	for _, f := range files {
		if err := w.AddFileFromBytes(f.path, f.data); err != nil {
			t.Fatalf("AddFileFromBytes(%s): %v", f.path, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p
}

func TestWriterOutOfOrderInsertion(t *testing.T) {
	files := []fileSpec{
		{"x.txt", []byte("x-content")},
		{"d/a", []byte("a-content, somewhat longer")},
		{"y.txt", []byte("y")},
		{"d/b", []byte("b-content")},
	}
	r, err := Open(writeArchiveFiles(t, files, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for _, f := range files {
		if got := readEntry(t, r, f.path); !bytes.Equal(got, f.data) {
			t.Errorf("%s = %q, want %q", f.path, got, f.data)
		}
	}
}

func TestWriterMethodNoneIsStored(t *testing.T) {
	content := bytes.Repeat([]byte("stored "), 20000) // more than one block
	p := writeArchiveFiles(t, []fileSpec{{"s.bin", content}}, func(w *Writer) { w.CompressionMethod = common.CompressionNone })
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	e := r.Find("s.bin")
	if e == nil || e.CompType != common.CompressionNone {
		t.Fatalf("entry = %+v, want a stored record", e)
	}
	if got := readEntry(t, r, "s.bin"); !bytes.Equal(got, content) {
		t.Error("stored content mismatch")
	}
	raw, err := r.ReadRawFileData(e)
	if err != nil || !bytes.Equal(raw, content) {
		t.Errorf("stored raw data should be the file itself (err %v)", err)
	}
}

func TestWriterRejectsUnknownMethod(t *testing.T) {
	w, err := CreateWriter(filepath.Join(t.TempDir(), "x.hpi"))
	if err != nil {
		t.Fatal(err)
	}
	w.CompressionMethod = 3
	if err := w.AddFileFromBytes("a.txt", []byte("a")); err == nil {
		t.Error("compression method 3 was accepted")
	}
	_ = w.Close()
}

// sqshTypes returns the SQSH type byte of every chunk of a chunked entry.
func sqshTypes(t *testing.T, r *Reader, path string) []byte {
	t.Helper()
	e := r.Find(path)
	raw, err := r.ReadRawFileData(e)
	if err != nil {
		t.Fatal(err)
	}
	n := int(chunkCount(e.Size))
	pos := n * 4
	var types []byte
	for i := 0; i < n; i++ {
		size := int(binary.LittleEndian.Uint32(raw[i*4:]))
		types = append(types, raw[pos+5])
		pos += size
	}
	return types
}

func TestWriterNeverEmitsStoredChunks(t *testing.T) {
	content := bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 20000)
	for _, method := range []uint8{common.CompressionLZ77, common.CompressionZLib} {
		p := writeArchiveFiles(t, []fileSpec{{"f.bin", content}}, func(w *Writer) { w.CompressionMethod = method })
		r, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		types := sqshTypes(t, r, "f.bin")
		if len(types) != 3 {
			t.Errorf("method %d: %d chunks, want 3", method, len(types))
		}
		for i, typ := range types {
			if typ != method {
				t.Errorf("method %d: chunk %d has SQSH type %d", method, i, typ)
			}
		}
		if got := readEntry(t, r, "f.bin"); !bytes.Equal(got, content) {
			t.Errorf("method %d: content mismatch", method)
		}
		_ = r.Close()
	}
}

func TestWriterMergesDirectoriesIgnoringCase(t *testing.T) {
	files := []fileSpec{
		{"Units/a.fbi", []byte("a")},
		{"units/b.fbi", []byte("b")},
		{`UNITS\c.fbi`, []byte("c")},
	}
	p := writeArchiveFiles(t, files, nil)
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if n := len(r.Root().Children); n != 1 {
		t.Fatalf("root has %d children, want one merged directory", n)
	}
	if name := r.Root().Children[0].Name; name != "Units" {
		t.Errorf("directory spelled %q, want the first spelling Units", name)
	}
	for _, f := range files {
		if got := readEntry(t, r, f.path); !bytes.Equal(got, f.data) {
			t.Errorf("%s = %q", f.path, got)
		}
	}
}

func TestWriterWarnsOnCollidingPaths(t *testing.T) {
	w, err := CreateWriter(filepath.Join(t.TempDir(), "x.hpi"))
	if err != nil {
		t.Fatal(err)
	}
	_ = w.AddFileFromBytes("maps/a.tnt", []byte("first"))
	_ = w.AddFileFromBytes("MAPS/A.TNT", []byte("second"))
	_ = w.AddFileFromBytes("units", []byte("file named like a dir"))
	_ = w.AddFileFromBytes("units/x.fbi", []byte("x"))
	warnings := w.Warnings()
	if len(warnings) != 2 {
		t.Fatalf("Warnings() = %q, want two", warnings)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(w.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, "maps/a.tnt"); string(got) != "second" {
		t.Errorf("maps/a.tnt = %q, want the copy added last", got)
	}
}

func TestWriterRejectsBadPaths(t *testing.T) {
	w, err := CreateWriter(filepath.Join(t.TempDir(), "x.hpi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "/x", "a//b", "a/./b", "a/../b", "../x", "x/", strings.Repeat("n", 256)} {
		if err := w.AddFileFromBytes(p, []byte("x")); err == nil {
			t.Errorf("AddFileFromBytes(%q) accepted a bad path", p)
		}
	}
	w.AddRawEntry("a//b", []byte{}, 0, 0)
	if err := w.Close(); err == nil {
		t.Error("Close should report the bad AddRawEntry path")
	}
}

func TestWriterBackslashPathsNest(t *testing.T) {
	r, err := Open(writeArchiveFiles(t, []fileSpec{{`units\arm\x.fbi`, []byte("x")}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := r.List(); len(got) != 1 || got[0] != "units/arm/x.fbi" {
		t.Errorf("List() = %q, want [units/arm/x.fbi]", got)
	}
}

func TestWriterRequiresGameTrailer(t *testing.T) {
	for _, trailer := range [][]byte{nil, []byte("Made with a tool"), []byte(common.DefaultTrailer + " ")} {
		p := filepath.Join(t.TempDir(), "x.hpi")
		w, err := CreateWriter(p)
		if err != nil {
			t.Fatal(err)
		}
		w.SetTrailer(trailer)
		_ = w.AddFileFromBytes("a.txt", []byte("a"))
		if err := w.Close(); !errors.Is(err, ErrInvalidTrailer) {
			t.Errorf("trailer %q: Close() = %v, want ErrInvalidTrailer", trailer, err)
		}
	}

	// Any year is fine.
	tr, _ := common.Trailer(2024)
	p := writeArchiveFiles(t, []fileSpec{{"a.txt", []byte("a")}}, func(w *Writer) { w.SetTrailer(tr) })
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.TrailerValid() {
		t.Error("a 2024 trailer should be valid")
	}
	_ = r.Close()

	// The explicit opt-out writes a trailerless archive the reader still opens.
	p = writeArchiveFiles(t, []fileSpec{{"a.txt", []byte("a")}}, func(w *Writer) {
		w.SetTrailer(nil)
		w.AllowNonGameTrailer = true
	})
	r, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if r.TrailerValid() {
		t.Error("trailerless archive reported a valid trailer")
	}
	if got := readEntry(t, r, "a.txt"); string(got) != "a" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestWriterHeaderKey0xFFWritesPlaintext(t *testing.T) {
	content := []byte("visible content")
	p := writeArchiveFiles(t, []fileSpec{{"plain.txt", content}}, func(w *Writer) {
		w.HeaderKey = 0xFF
		w.CompressionMethod = common.CompressionNone
	})
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("plain.txt\x00")) || !bytes.Contains(raw, content) {
		t.Error("header key 0xFF should leave the archive unencrypted")
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, "plain.txt"); !bytes.Equal(got, content) {
		t.Errorf("got %q", got)
	}
}

func TestWriterRawPassthroughStoredAndChunked(t *testing.T) {
	src := writeArchiveFiles(t, []fileSpec{{"c.bin", bytes.Repeat([]byte("chunk"), 30000)}}, nil)
	stored := writeArchiveFiles(t, []fileSpec{{"s.bin", []byte("stored")}}, func(w *Writer) { w.CompressionMethod = 0 })

	out := filepath.Join(t.TempDir(), "copy.hpi")
	w, err := CreateWriter(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{src, stored} {
		r, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range r.List() {
			e := r.Find(name)
			raw, err := r.ReadRawFileData(e)
			if err != nil {
				t.Fatal(err)
			}
			w.AddRawEntry(name, raw, e.Size, e.CompType)
		}
		_ = r.Close()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := readEntry(t, r, "c.bin"); !bytes.Equal(got, bytes.Repeat([]byte("chunk"), 30000)) {
		t.Error("chunked passthrough mismatch")
	}
	if got := readEntry(t, r, "s.bin"); string(got) != "stored" {
		t.Errorf("stored passthrough = %q", got)
	}
}
