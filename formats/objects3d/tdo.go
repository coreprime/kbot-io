package objects3d

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// On-disk record sizes.
const (
	objectHeaderSize    = 52 // 13 little-endian int32 words
	primitiveRecordSize = 32 // 8 little-endian int32 words
	vertexRecordSize    = 12 // 3 little-endian int32 words
	vertexIndexSize     = 2  // one little-endian uint16
)

// maxStringLen bounds the search for the NUL that ends an object or texture
// name.
const maxStringLen = 4096

// Decode budget: headers and arrays may overlap one another, but the records
// decoded from a file may not add up to more than this many times the file
// size, plus a fixed allowance. A file whose parts do not overlap decodes at
// most its own size.
const (
	decodeBudgetFactor = 4
	decodeBudgetSlack  = 1 << 20
)

// ColoredFlag is the bit of a primitive's stored is_colored word that makes
// the game fill the primitive with its colour index instead of texturing it.
const ColoredFlag = 0x1

// MissingTextureColor is the palette index TA 3.1c fills a primitive with
// when its texture name does not resolve to a texture.
const MissingTextureColor = 0xd1

// ErrMalformed is wrapped by every error LoadFromBytes and LoadFromReader
// return for a file that is truncated or structurally damaged. Errors for data
// that runs past the end of the file also wrap io.ErrUnexpectedEOF.
var ErrMalformed = errors.New("malformed 3DO file")

// Vertex is a 3D point in 16.16 fixed point (65536 units = one world pixel).
type Vertex struct {
	X, Y, Z int32
}

// Primitive is one face, line or point of an object.
type Primitive struct {
	// ColorIndex is the palette index a coloured primitive is filled with.
	// The game uses only the low byte of the stored colour word, so a loaded
	// primitive has ColorIndex == RawColorIndex & 0xff.
	ColorIndex int
	// VertexIndices index the owning object's Vertices, in the stored corner
	// order. They are kept as stored even when one is past the end of
	// Vertices: the game does not check them, and the renderer skips such a
	// primitive.
	VertexIndices []int
	// TextureName is the stored texture name, or "" for none. The game looks
	// texture names up case-insensitively.
	TextureName string
	// IsColored is bit 0 (ColoredFlag) of the stored is_colored word: the game
	// fills the primitive with ColorIndex instead of texturing it.
	IsColored bool
	// Synthetic marks primitives that were not present in the source file
	// but were generated to close gaps left by deleted faces. It is never
	// set by the loader; only FillModel produces synthetic primitives.
	Synthetic bool
	// RawColorIndex is the colour word exactly as stored in the file.
	RawColorIndex int32
	// RawIsColored is the is_colored flag word exactly as stored in the
	// file. The game keeps the whole word as the primitive's flags and sets
	// its own animated (0x2) and team-texture (0x4) bits from the texture it
	// binds; only bit 0 changes how a primitive is drawn.
	RawIsColored int32
}

// Object is a node (piece) in the 3DO hierarchy.
type Object struct {
	Name       string
	Vertices   []Vertex
	Primitives []Primitive
	// XFromParent, YFromParent and ZFromParent are the piece's offset from
	// its parent, in 16.16 fixed point.
	XFromParent int32
	YFromParent int32
	ZFromParent int32
	// SelectionPrim is the stored selection primitive index; -1 means none.
	// Shipped child pieces also carry stale values. HiddenPrimitive applies
	// the game's rule for which primitive it never draws.
	SelectionPrim int32
	// Children are the objects on this object's child chain, in file order.
	Children []*Object
	// VersionSignature is the first word of the object header, as stored.
	// Retail models store 1; the loader does not check it.
	VersionSignature int32
}

// Model is a parsed 3DO file.
type Model struct {
	// Root is the object whose header starts the file.
	Root *Object
	// RootSiblings are the objects on the root's sibling chain, in file
	// order. The game loads them, with their children, as further top-level
	// pieces after the root's subtree. TA 3.1c's piece transform starts at
	// the root and never reaches them, so it applies no piece offsets to a
	// root sibling or its descendants: they are drawn at their own vertex
	// coordinates.
	RootSiblings []*Object
	// AllObjects lists every object in preorder: the root, its subtree, then
	// each root sibling followed by its subtree. This is the order in which
	// the game numbers a model's pieces before a unit script's piece list is
	// bound to them.
	AllObjects []*Object
}

// TotalVertices returns the total vertex count across all objects.
func (m *Model) TotalVertices() int {
	n := 0
	for _, o := range m.AllObjects {
		n += len(o.Vertices)
	}
	return n
}

// TotalPrimitives returns the total primitive count across all objects.
func (m *Model) TotalPrimitives() int {
	n := 0
	for _, o := range m.AllObjects {
		n += len(o.Primitives)
	}
	return n
}

// Textures returns the unique texture names used across all objects, in
// first-use order. Names are compared case-insensitively (ASCII), as the game
// looks them up; each name is reported with the spelling of its first use.
func (m *Model) Textures() []string {
	seen := make(map[string]bool)
	var textures []string
	for _, o := range m.AllObjects {
		for _, p := range o.Primitives {
			if p.TextureName == "" {
				continue
			}
			key := foldName(p.TextureName)
			if !seen[key] {
				seen[key] = true
				textures = append(textures, p.TextureName)
			}
		}
	}
	return textures
}

// TopLevel returns the root followed by its siblings: the heads of the
// model's top-level subtrees. It returns nil for a model without a root.
func (m *Model) TopLevel() []*Object {
	if m == nil || m.Root == nil {
		return nil
	}
	return append([]*Object{m.Root}, m.RootSiblings...)
}

// foldName lower-cases ASCII letters only, matching the game's name lookups.
func foldName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// LoadFromReader parses a 3DO file. The model is read from offset 0 of r to
// its end, whatever r's current position. See LoadFromBytes for the rules.
func LoadFromReader(r io.ReadSeeker) (*Model, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read 3do: %w", err)
	}
	return LoadFromBytes(data)
}

// LoadFromBytes parses a 3DO file held in memory.
//
// Offsets of 0 mean "none" for names, texture names and sibling and child
// links; an array with a count of 0 is never read. Everything else the file
// points at must lie inside it. The load fails, with an error wrapping
// ErrMalformed, for a truncated root header, a header, array or string that
// runs past the end of the file, a negative count or offset, a non-empty
// array at offset 0, a string with no NUL within 4096 bytes, an object header
// reached twice (a link cycle or a shared subtree), or overlapping arrays that
// decode to more than four times the file size. Vertex indices past an
// object's vertex count and any version signature are accepted.
func LoadFromBytes(data []byte) (*Model, error) {
	if len(data) < objectHeaderSize {
		return nil, fmt.Errorf("read root object: %w", truncated(len(data)))
	}
	l := &loader{
		data:    data,
		seen:    make(map[int64]bool),
		strings: make(map[int64]string),
		budget:  int64(len(data))*decodeBudgetFactor + decodeBudgetSlack,
	}
	return l.load()
}

// loader decodes one file. Objects are visited with an explicit stack, so a
// long link chain cannot exhaust the goroutine stack.
type loader struct {
	data    []byte
	seen    map[int64]bool   // object header offsets already loaded
	strings map[int64]string // strings already read, by offset
	budget  int64            // bytes of records still allowed to decode
}

// pendingObject is an object header still to be loaded.
type pendingObject struct {
	at     int64
	parent *Object // nil for the root and its siblings
	from   int64   // header whose link points here, -1 for the root
}

func (l *loader) load() (*Model, error) {
	m := &Model{}
	stack := []pendingObject{{at: 0, from: -1}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if l.seen[p.at] {
			return nil, fmt.Errorf("3do: object at 0x%x links to object at 0x%x, which is already loaded (a cycle or a shared subtree): %w",
				p.from, p.at, ErrMalformed)
		}
		l.seen[p.at] = true
		obj, sibling, child, err := l.object(p.at)
		if err != nil {
			return nil, err
		}
		m.AllObjects = append(m.AllObjects, obj)
		switch {
		case p.parent != nil:
			p.parent.Children = append(p.parent.Children, obj)
		case m.Root == nil:
			m.Root = obj
		default:
			m.RootSiblings = append(m.RootSiblings, obj)
		}
		// The child is pushed last so its subtree is loaded before the
		// sibling, giving preorder.
		if sibling != 0 {
			stack = append(stack, pendingObject{at: sibling, parent: p.parent, from: p.at})
		}
		if child != 0 {
			stack = append(stack, pendingObject{at: child, parent: obj, from: p.at})
		}
	}
	return m, nil
}

func (l *loader) word(off int64) int32 {
	return int32(binary.LittleEndian.Uint32(l.data[off:]))
}

// object decodes the object whose header is at at and returns it with its
// sibling and child link offsets.
func (l *loader) object(at int64) (obj *Object, sibling, child int64, err error) {
	if err := l.region(at, 1, objectHeaderSize, true); err != nil {
		return nil, 0, 0, fmt.Errorf("3do: object header at 0x%x: %w", at, err)
	}
	var h [13]int32
	for i := range h {
		h[i] = l.word(at + int64(i)*4)
	}
	obj = &Object{
		VersionSignature: h[0],
		SelectionPrim:    h[3],
		XFromParent:      h[4],
		YFromParent:      h[5],
		ZFromParent:      h[6],
	}
	if nameAt := int64(h[7]); nameAt != 0 {
		if obj.Name, err = l.str(nameAt); err != nil {
			return nil, 0, 0, fmt.Errorf("3do: name of object at 0x%x: %w", at, err)
		}
	}

	nv, vertAt := int64(h[1]), int64(h[9])
	if err := l.region(vertAt, nv, vertexRecordSize, false); err != nil {
		return nil, 0, 0, fmt.Errorf("3do: vertex array of object at 0x%x (%d vertices at 0x%x): %w", at, nv, vertAt, err)
	}
	if nv > 0 {
		obj.Vertices = make([]Vertex, nv)
		for i := range obj.Vertices {
			off := vertAt + int64(i)*vertexRecordSize
			obj.Vertices[i] = Vertex{X: l.word(off), Y: l.word(off + 4), Z: l.word(off + 8)}
		}
	}

	np, primAt := int64(h[2]), int64(h[10])
	if err := l.region(primAt, np, primitiveRecordSize, false); err != nil {
		return nil, 0, 0, fmt.Errorf("3do: primitive array of object at 0x%x (%d primitives at 0x%x): %w", at, np, primAt, err)
	}
	if np > 0 {
		obj.Primitives = make([]Primitive, np)
		for i := range obj.Primitives {
			if err := l.primitive(primAt+int64(i)*primitiveRecordSize, &obj.Primitives[i]); err != nil {
				return nil, 0, 0, fmt.Errorf("3do: primitive %d of object at 0x%x: %w", i, at, err)
			}
		}
	}
	return obj, int64(h[11]), int64(h[12]), nil
}

// primitive decodes the primitive record at off into p.
func (l *loader) primitive(off int64, p *Primitive) error {
	colour := l.word(off)
	n, indexAt := int64(l.word(off+4)), int64(l.word(off+12))
	textureAt := int64(l.word(off + 16))
	flags := l.word(off + 28)
	*p = Primitive{
		ColorIndex:    int(uint8(colour)),
		IsColored:     flags&ColoredFlag != 0,
		RawColorIndex: colour,
		RawIsColored:  flags,
	}
	if textureAt != 0 {
		name, err := l.str(textureAt)
		if err != nil {
			return fmt.Errorf("texture name: %w", err)
		}
		p.TextureName = name
	}
	if err := l.region(indexAt, n, vertexIndexSize, false); err != nil {
		return fmt.Errorf("vertex index array (%d indices at 0x%x): %w", n, indexAt, err)
	}
	if n > 0 {
		// Vertex indices are unsigned 16-bit values.
		p.VertexIndices = make([]int, n)
		for j := range p.VertexIndices {
			p.VertexIndices[j] = int(binary.LittleEndian.Uint16(l.data[indexAt+int64(j)*vertexIndexSize:]))
		}
	}
	return nil
}

// region checks that count records of stride bytes at off lie inside the file
// and charges them to the decode budget. A count of 0 is never checked.
// Offset 0 is legal only for an object header (the root's).
func (l *loader) region(off, count, stride int64, header bool) error {
	if count == 0 {
		return nil
	}
	if count < 0 {
		return fmt.Errorf("negative count: %w", ErrMalformed)
	}
	if off == 0 && !header {
		return fmt.Errorf("non-empty array at offset 0: %w", ErrMalformed)
	}
	if off < 0 {
		return fmt.Errorf("negative offset: %w", ErrMalformed)
	}
	size := int64(len(l.data))
	if off > size || count > (size-off)/stride {
		return truncated(len(l.data))
	}
	l.budget -= count * stride
	if l.budget < 0 {
		return fmt.Errorf("overlapping arrays decode to more than %d times the file size: %w", decodeBudgetFactor, ErrMalformed)
	}
	return nil
}

// str reads the NUL-terminated string at off. Strings are cached by offset,
// so names shared by many primitives are decoded once.
func (l *loader) str(off int64) (string, error) {
	if s, ok := l.strings[off]; ok {
		return s, nil
	}
	if off < 0 {
		return "", fmt.Errorf("negative offset: %w", ErrMalformed)
	}
	if off >= int64(len(l.data)) {
		return "", fmt.Errorf("offset 0x%x: %w", off, truncated(len(l.data)))
	}
	rest := l.data[off:]
	if len(rest) > maxStringLen {
		rest = rest[:maxStringLen]
	}
	end := bytes.IndexByte(rest, 0)
	switch {
	case end >= 0:
	case len(rest) == maxStringLen:
		return "", fmt.Errorf("string at 0x%x has no NUL within %d bytes: %w", off, maxStringLen, ErrMalformed)
	default:
		return "", fmt.Errorf("string at 0x%x has no NUL before the end of the file: %w", off, truncated(len(l.data)))
	}
	s := string(rest[:end])
	l.strings[off] = s
	return s, nil
}

// truncated is the error for data that runs past the end of a size-byte file.
func truncated(size int) error {
	return fmt.Errorf("runs past the end of the %d-byte file: %w (%w)", size, ErrMalformed, io.ErrUnexpectedEOF)
}
