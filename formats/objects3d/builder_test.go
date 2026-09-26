package objects3d

import "encoding/binary"

// fileBuilder assembles synthetic 3DO bytes for tests.
type fileBuilder struct{ buf []byte }

// alloc appends n zero bytes and returns their offset.
func (b *fileBuilder) alloc(n int) int {
	at := len(b.buf)
	b.buf = append(b.buf, make([]byte, n)...)
	return at
}

func (b *fileBuilder) put32(at int, v int32) {
	binary.LittleEndian.PutUint32(b.buf[at:], uint32(v))
}

// hdr holds the 13 words of an object header.
type hdr struct {
	version, nVerts, nPrims, sel int32
	x, y, z                      int32
	name, always0, verts, prims  int32
	sibling, child               int32
}

func (b *fileBuilder) header(at int, h hdr) {
	for i, v := range []int32{h.version, h.nVerts, h.nPrims, h.sel, h.x, h.y, h.z,
		h.name, h.always0, h.verts, h.prims, h.sibling, h.child} {
		b.put32(at+4*i, v)
	}
}

// str appends a NUL-terminated string and returns its offset.
func (b *fileBuilder) str(s string) int32 {
	at := b.alloc(len(s) + 1)
	copy(b.buf[at:], s)
	return int32(at)
}

func (b *fileBuilder) vertices(vs ...Vertex) int32 {
	at := b.alloc(len(vs) * vertexRecordSize)
	for i, v := range vs {
		b.put32(at+12*i, v.X)
		b.put32(at+12*i+4, v.Y)
		b.put32(at+12*i+8, v.Z)
	}
	return int32(at)
}

func (b *fileBuilder) indices(idx ...uint16) int32 {
	at := b.alloc(len(idx) * vertexIndexSize)
	for i, v := range idx {
		binary.LittleEndian.PutUint16(b.buf[at+2*i:], v)
	}
	return int32(at)
}

// rawPrim holds the 8 words of a primitive record.
type rawPrim struct {
	colour, count, always0, indices, texture, unknown1, unknown2, isColored int32
}

func (b *fileBuilder) prim(at int, p rawPrim) {
	for i, v := range []int32{p.colour, p.count, p.always0, p.indices, p.texture,
		p.unknown1, p.unknown2, p.isColored} {
		b.put32(at+4*i, v)
	}
}

// tObj describes an object for encodeTree.
type tObj struct {
	name     string
	version  int32
	sel      int32
	offset   [3]int32
	verts    []Vertex
	prims    []tPrim
	children []*tObj
}

// tPrim describes a primitive for encodeTree.
type tPrim struct {
	colour, isColored int32
	idx               []uint16
	texture           string
}

// encodeTree lays out a well-formed file: the headers of every object in
// preorder (the first top-level object at offset 0), then each object's data.
// top is the root followed by its siblings.
func encodeTree(top ...*tObj) []byte {
	b := &fileBuilder{}
	at := map[*tObj]int{}
	var place func(chain []*tObj)
	place = func(chain []*tObj) {
		for _, o := range chain {
			at[o] = b.alloc(objectHeaderSize)
			place(o.children)
		}
	}
	place(top)
	links := func(chain []*tObj) {
		for i, o := range chain {
			h := hdr{version: o.version, sel: o.sel, x: o.offset[0], y: o.offset[1], z: o.offset[2]}
			if o.version == 0 {
				h.version = 1
			}
			if i+1 < len(chain) {
				h.sibling = int32(at[chain[i+1]])
			}
			if len(o.children) > 0 {
				h.child = int32(at[o.children[0]])
			}
			if o.name != "" {
				h.name = b.str(o.name)
			}
			if len(o.verts) > 0 {
				h.nVerts = int32(len(o.verts))
				h.verts = b.vertices(o.verts...)
			}
			if len(o.prims) > 0 {
				h.nPrims = int32(len(o.prims))
				h.prims = int32(b.alloc(len(o.prims) * primitiveRecordSize))
				for k, p := range o.prims {
					rp := rawPrim{colour: p.colour, isColored: p.isColored, count: int32(len(p.idx))}
					if len(p.idx) > 0 {
						rp.indices = b.indices(p.idx...)
					}
					if p.texture != "" {
						rp.texture = b.str(p.texture)
					}
					b.prim(int(h.prims)+k*primitiveRecordSize, rp)
				}
			}
			b.header(at[o], h)
		}
	}
	var linkAll func(chain []*tObj)
	linkAll = func(chain []*tObj) {
		links(chain)
		for _, o := range chain {
			linkAll(o.children)
		}
	}
	linkAll(top)
	return b.buf
}

// colouredCube is a cube centred at the origin whose six quads are coloured
// and wound so that their right-hand-rule normals point outward.
func colouredCube(s int32) *Object {
	faces := [][]int{
		{0, 3, 2, 1}, // -Z
		{4, 5, 6, 7}, // +Z
		{0, 1, 5, 4}, // -Y
		{3, 7, 6, 2}, // +Y
		{1, 2, 6, 5}, // +X
		{0, 4, 7, 3}, // -X
	}
	o := &Object{
		Vertices: []Vertex{
			{-s, -s, -s}, {s, -s, -s}, {s, s, -s}, {-s, s, -s},
			{-s, -s, s}, {s, -s, s}, {s, s, s}, {-s, s, s},
		},
		SelectionPrim: -1,
	}
	for _, f := range faces {
		o.Primitives = append(o.Primitives, Primitive{VertexIndices: f, IsColored: true})
	}
	return o
}
