package objects3d

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func names(objs []*Object) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.Name
	}
	return out
}

func mustLoad(t *testing.T, data []byte) *Model {
	t.Helper()
	m, err := LoadFromBytes(data)
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	return m
}

// wantMalformed checks that data fails to load with an error that wraps
// ErrMalformed and mentions fragment.
func wantMalformed(t *testing.T, data []byte, fragment string) {
	t.Helper()
	_ = malformedErr(t, data, fragment)
}

// malformedErr is wantMalformed returning the error.
func malformedErr(t *testing.T, data []byte, fragment string) error {
	t.Helper()
	m, err := LoadFromBytes(data)
	if err == nil {
		t.Fatalf("LoadFromBytes succeeded (%d objects), want an error", len(m.AllObjects))
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("error %q does not wrap ErrMalformed", err)
	}
	if !strings.Contains(err.Error(), fragment) {
		t.Errorf("error %q does not mention %q", err, fragment)
	}
	return err
}

// The root's sibling chain is part of the model: its objects follow the
// root's subtree in AllObjects and are listed in RootSiblings.
func TestLoadRootSiblings(t *testing.T) {
	data := encodeTree(
		&tObj{name: "base", children: []*tObj{{name: "turret"}, {name: "flare"}}},
		&tObj{name: "extra", children: []*tObj{{name: "extrachild"}}},
		&tObj{name: "extra2"},
	)
	m := mustLoad(t, data)
	if m.Root.Name != "base" {
		t.Fatalf("root = %q", m.Root.Name)
	}
	if got, want := names(m.RootSiblings), []string{"extra", "extra2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RootSiblings = %v, want %v", got, want)
	}
	if got, want := names(m.AllObjects), []string{"base", "turret", "flare", "extra", "extrachild", "extra2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllObjects = %v, want %v", got, want)
	}
	if got, want := names(m.RootSiblings[0].Children), []string{"extrachild"}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra children = %v, want %v", got, want)
	}
	if got, want := names(m.TopLevel()), []string{"base", "extra", "extra2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TopLevel = %v, want %v", got, want)
	}
}

// A child link back to an ancestor is an error, not endless recursion.
func TestLoadChildCycle(t *testing.T) {
	// An object whose child link points at itself.
	b := &fileBuilder{}
	b.alloc(2 * objectHeaderSize)
	b.header(0, hdr{version: 1, child: 52})
	b.header(52, hdr{version: 1, child: 52})
	wantMalformed(t, b.buf, "already loaded")

	// A grandchild whose child link points back at its parent.
	b = &fileBuilder{}
	b.alloc(3 * objectHeaderSize)
	b.header(0, hdr{version: 1, child: 52})
	b.header(52, hdr{version: 1, child: 104})
	b.header(104, hdr{version: 1, child: 52})
	wantMalformed(t, b.buf, "already loaded")
}

// Sibling cycles are errors too, rather than being cut silently.
func TestLoadSiblingCycle(t *testing.T) {
	b := &fileBuilder{}
	b.alloc(3 * objectHeaderSize)
	b.header(0, hdr{version: 1, child: 52})
	b.header(52, hdr{version: 1, sibling: 104})
	b.header(104, hdr{version: 1, sibling: 52})
	wantMalformed(t, b.buf, "already loaded")
}

// Two links to one subtree are rejected instead of expanding it once per
// path; 40 levels of doubly-linked headers would otherwise build 2^40 objects.
func TestLoadSharedSubtree(t *testing.T) {
	const levels = 40
	b := &fileBuilder{}
	b.alloc(levels * objectHeaderSize)
	for i := 0; i < levels-1; i++ {
		next := int32((i + 1) * objectHeaderSize)
		b.header(i*objectHeaderSize, hdr{version: 1, child: next})
		if i > 0 {
			b.put32(i*objectHeaderSize+44, next) // sibling -> the same object
		}
	}
	b.header((levels-1)*objectHeaderSize, hdr{version: 1})
	done := make(chan error, 1)
	go func() {
		_, err := LoadFromBytes(b.buf)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load did not finish")
	}
}

// A long sibling chain loads without recursion.
func TestLoadLongSiblingChain(t *testing.T) {
	const n = 100000
	b := &fileBuilder{}
	b.alloc((n + 1) * objectHeaderSize)
	b.header(0, hdr{version: 1, child: objectHeaderSize})
	for i := 1; i <= n; i++ {
		h := hdr{version: 1}
		if i < n {
			h.sibling = int32((i + 1) * objectHeaderSize)
		}
		b.header(i*objectHeaderSize, h)
	}
	m := mustLoad(t, b.buf)
	if len(m.Root.Children) != n || len(m.AllObjects) != n+1 {
		t.Fatalf("children = %d, objects = %d", len(m.Root.Children), len(m.AllObjects))
	}
}

// Truncated data is an error instead of zero-filled or dropped geometry.
func TestLoadTruncated(t *testing.T) {
	full := encodeTree(&tObj{
		name:  "base",
		verts: []Vertex{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
		prims: []tPrim{{idx: []uint16{0, 1, 2}, texture: "armtex"}},
		children: []*tObj{{
			name:  "child",
			verts: []Vertex{{1, 1, 1}},
		}},
	})
	mustLoad(t, full)

	cases := []struct {
		name  string
		data  func() []byte
		match string
	}{
		{"short root header", func() []byte { return full[:40] }, "root object"},
		{"cut file", func() []byte { return full[:len(full)-3] }, "past the end"},
		{"vertex array past end", func() []byte {
			d := append([]byte(nil), full...)
			d[4] = 200 // 200 vertices
			return d
		}, "vertex array"},
		{"primitive array past end", func() []byte {
			d := append([]byte(nil), full...)
			d[8] = 50
			return d
		}, "primitive array"},
		{"child header past end", func() []byte {
			d := append([]byte(nil), full...)
			(&fileBuilder{buf: d}).put32(48, int32(len(d)-10))
			return d
		}, "object header"},
		{"name past end", func() []byte {
			d := append([]byte(nil), full...)
			(&fileBuilder{buf: d}).put32(28, int32(len(d)+5))
			return d
		}, "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := malformedErr(t, c.data(), c.match)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("error %q does not wrap io.ErrUnexpectedEOF", err)
			}
		})
	}
}

// Header counts are checked against the file before anything is allocated.
func TestLoadHugeCounts(t *testing.T) {
	b := &fileBuilder{}
	b.alloc(objectHeaderSize)
	b.header(0, hdr{version: 1, nVerts: 0x7fffffff, verts: 52})
	wantMalformed(t, b.buf, "vertex array")
	b.header(0, hdr{version: 1, nPrims: 0x7fffffff, prims: 52})
	wantMalformed(t, b.buf, "primitive array")
}

func TestLoadBadCountsAndOffsets(t *testing.T) {
	base := func() *fileBuilder {
		b := &fileBuilder{}
		b.alloc(objectHeaderSize)
		return b
	}
	b := base()
	b.header(0, hdr{version: 1, nVerts: -1, verts: 52})
	wantMalformed(t, b.buf, "negative count")

	b = base()
	b.header(0, hdr{version: 1, nVerts: 1})
	b.vertices(Vertex{})
	wantMalformed(t, b.buf, "offset 0")

	b = base()
	b.header(0, hdr{version: 1, nVerts: 1, verts: -12})
	wantMalformed(t, b.buf, "negative offset")

	b = base()
	b.header(0, hdr{version: 1, child: -52})
	wantMalformed(t, b.buf, "negative offset")

	// An empty array is never read, whatever its offset.
	b = base()
	b.header(0, hdr{version: 1, verts: -1, prims: 0x7ffffff0})
	m := mustLoad(t, b.buf)
	if len(m.Root.Vertices) != 0 || len(m.Root.Primitives) != 0 {
		t.Errorf("empty arrays decoded: %+v", m.Root)
	}
}

func TestLoadStrings(t *testing.T) {
	b := &fileBuilder{}
	b.alloc(objectHeaderSize)
	long := strings.Repeat("n", 1000)
	b.header(0, hdr{version: 1, name: b.str(long)})
	if m := mustLoad(t, b.buf); m.Root.Name != long {
		t.Errorf("1000-byte name not kept whole (len %d)", len(m.Root.Name))
	}

	b = &fileBuilder{}
	b.alloc(objectHeaderSize)
	b.header(0, hdr{version: 1, name: b.str(strings.Repeat("n", maxStringLen+10))})
	wantMalformed(t, b.buf, "no NUL within")

	b = &fileBuilder{}
	b.alloc(objectHeaderSize)
	at := b.alloc(3)
	copy(b.buf[at:], "abc") // no terminator before the end of the file
	b.header(0, hdr{version: 1, name: int32(at)})
	wantMalformed(t, b.buf, "no NUL before the end")
}

// Arrays shared by many primitives are allowed, up to a budget tied to the
// file size, so a small file cannot demand gigabytes of decoded indices.
func TestLoadAliasBudget(t *testing.T) {
	build := func(prims int, count int) []byte {
		b := &fileBuilder{}
		b.alloc(objectHeaderSize)
		primAt := b.alloc(prims * primitiveRecordSize)
		idxAt := b.alloc(count * vertexIndexSize)
		for i := 0; i < prims; i++ {
			b.prim(primAt+i*primitiveRecordSize, rawPrim{count: int32(count), indices: int32(idxAt)})
		}
		b.header(0, hdr{version: 1, nPrims: int32(prims), prims: int32(primAt)})
		return b.buf
	}
	// Four primitives sharing one index array decode fine.
	if m := mustLoad(t, build(4, 1000)); len(m.Root.Primitives[3].VertexIndices) != 1000 {
		t.Fatal("shared index array not decoded")
	}
	wantMalformed(t, build(64, 256*1024), "overlapping arrays")
}

// The version word is kept and not checked.
func TestLoadVersionSignature(t *testing.T) {
	for _, v := range []int32{0, 1, 7} {
		b := &fileBuilder{}
		b.alloc(objectHeaderSize)
		b.header(0, hdr{version: v})
		if m := mustLoad(t, b.buf); m.Root.VersionSignature != v {
			t.Errorf("VersionSignature = %d, want %d", m.Root.VersionSignature, v)
		}
	}
}

// An index past the vertex count is kept as stored.
func TestLoadOutOfRangeIndexKept(t *testing.T) {
	data := encodeTree(&tObj{
		verts: []Vertex{{}, {}, {}},
		prims: []tPrim{{idx: []uint16{0, 1, 3}}},
	})
	m := mustLoad(t, data)
	if got := m.Root.Primitives[0].VertexIndices; !reflect.DeepEqual(got, []int{0, 1, 3}) {
		t.Errorf("indices = %v", got)
	}
}

// LoadFromReader reads the whole stream from offset 0, whatever its position.
func TestLoadFromReaderMatchesBytes(t *testing.T) {
	data := encodeTree(&tObj{name: "a", verts: []Vertex{{1, 2, 3}}, children: []*tObj{{name: "b"}}})
	r := bytes.NewReader(data)
	_, _ = r.Seek(30, io.SeekStart)
	m1, err := LoadFromReader(r)
	if err != nil {
		t.Fatal(err)
	}
	m2 := mustLoad(t, data)
	if !reflect.DeepEqual(m1, m2) {
		t.Error("LoadFromReader and LoadFromBytes disagree")
	}
}
