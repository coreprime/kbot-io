package v2

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// rawV2 assembles a v2 archive from a directory block and a name block.
func rawV2(dir, names []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(common.HeaderMarker))
	_ = binary.Write(&b, binary.LittleEndian, common.VersionV2)
	dirOff := int32(8 + headerV2Size)
	_ = binary.Write(&b, binary.LittleEndian, headerV2{
		DirectoryBlock: dirOff,
		DirectorySize:  int32(len(dir)),
		NameBlock:      dirOff + int32(len(dir)),
		NameSize:       int32(len(names)),
		Data:           0x20,
	})
	b.Write(dir)
	b.Write(names)
	return b.Bytes()
}

func dirRecord(namePtr, firstSub, subCount, firstFile, fileCount int32) []byte {
	var b bytes.Buffer
	for _, v := range []int32{namePtr, firstSub, subCount, firstFile, fileCount} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}

func TestReaderRejectsDirectoryCycle(t *testing.T) {
	names := []byte("\x00loop\x00")
	// Root (offset 0) has one subdirectory at 20, whose own subdirectory
	// array points back at itself.
	dir := append(dirRecord(0, 20, 1, 0, 0), dirRecord(1, 20, 1, 0, 0)...)
	p := filepath.Join(t.TempDir(), "cycle.hpi")
	if err := os.WriteFile(p, rawV2(dir, names), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(p); err == nil {
		_ = r.Close()
		t.Fatal("a directory cycle was accepted")
	}
}

func TestWriterMergesDirectoriesAndChecksPaths(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.hpi")
	w, err := CreateWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "/a", "a//b", "a/../b", "./a"} {
		if err := w.AddFileFromBytes(bad, []byte("x")); err == nil {
			t.Errorf("AddFileFromBytes(%q) accepted a bad path", bad)
		}
	}
	files := map[string]string{"Units/a.fbi": "a", `units\b.fbi`: "b"}
	for path, data := range files {
		if err := w.AddFileFromBytes(path, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if n := len(r.Root().Children); n != 1 {
		t.Fatalf("root has %d children, want one merged directory", n)
	}
	for path, want := range files {
		rc, err := r.Open(path)
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}
