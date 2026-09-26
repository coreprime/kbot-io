package objects3d

import (
	"reflect"
	"testing"
)

func TestPrimitiveStyle(t *testing.T) {
	tri, quad, pent := []int{0, 1, 2}, []int{0, 1, 2, 3}, []int{0, 1, 2, 3, 4}
	cases := []struct {
		name  string
		p     Primitive
		found bool
		style FaceStyle
		index uint8
	}{
		{"coloured quad", Primitive{VertexIndices: quad, IsColored: true, ColorIndex: 0x42}, true, FaceFilled, 0x42},
		{"coloured pentagon", Primitive{VertexIndices: pent, IsColored: true, ColorIndex: 9}, true, FaceFilled, 9},
		{"textured quad", Primitive{VertexIndices: quad, TextureName: "t"}, true, FaceTextured, 0},
		{"textured triangle", Primitive{VertexIndices: tri, TextureName: "t"}, true, FaceHidden, 0},
		{"textured pentagon", Primitive{VertexIndices: pent, TextureName: "t"}, true, FaceHidden, 0},
		{"missing texture quad", Primitive{VertexIndices: quad, TextureName: "t"}, false, FaceFilled, MissingTextureColor},
		{"missing texture triangle", Primitive{VertexIndices: tri, TextureName: "t"}, false, FaceFilled, MissingTextureColor},
		{"missing texture, coloured", Primitive{VertexIndices: quad, TextureName: "t", IsColored: true, ColorIndex: 3}, false, FaceFilled, MissingTextureColor},
		{"coloured with found texture", Primitive{VertexIndices: quad, TextureName: "t", IsColored: true, ColorIndex: 3}, true, FaceFilled, 3},
		{"untextured uncoloured", Primitive{VertexIndices: quad}, true, FaceHidden, 0},
		{"untextured uncoloured, not found", Primitive{VertexIndices: quad}, false, FaceHidden, 0},
		{"coloured line", Primitive{VertexIndices: []int{0, 1}, IsColored: true}, true, FaceHidden, 0},
	}
	for _, c := range cases {
		style, index := c.p.Style(c.found)
		if style != c.style || index != c.index {
			t.Errorf("%s: Style(%v) = %v, %#x; want %v, %#x", c.name, c.found, style, index, c.style, c.index)
		}
	}
}

func TestHiddenPrimitive(t *testing.T) {
	three := make([]Primitive, 3)
	cases := []struct {
		sel   int32
		prims []Primitive
		want  int
	}{
		{-1, three, -1},
		{0, three, 0},
		{2, three, 2},
		{3, three, 0},  // stale index past the end
		{7, three, 0},  // stale index past the end
		{-5, three, 0}, // negative, not -1
		{2, nil, -1},   // no primitives
	}
	for _, c := range cases {
		o := &Object{SelectionPrim: c.sel, Primitives: c.prims}
		if got := o.HiddenPrimitive(); got != c.want {
			t.Errorf("sel %d with %d primitives: HiddenPrimitive = %d, want %d", c.sel, len(c.prims), got, c.want)
		}
	}
}

func TestDrawOrder(t *testing.T) {
	// Vertex Y values; each primitive's mean Y is given by its single corner
	// unless noted.
	verts := []Vertex{{Y: 50}, {Y: 10}, {Y: 30}, {Y: 10}, {Y: 20}, {Y: -7}}
	prim := func(idx ...int) Primitive { return Primitive{VertexIndices: idx} }
	o := &Object{
		Vertices: verts,
		Primitives: []Primitive{
			prim(0),       // 0: mean 50
			prim(1),       // 1: mean 10
			prim(2),       // 2: mean 30
			prim(3),       // 3: mean 10 (ties with 1, keeps order)
			prim(4, 5, 5), // 4: (20-7-7)/3 = 2
			prim(0, 99),   // 5: out-of-range corner adds nothing: 50/2 = 25
			prim(),        // 6: no corners, mean 0
		},
		SelectionPrim: -1,
	}
	if got, want := o.DrawOrder(), []int{0, 6, 4, 1, 3, 5, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("no selection: DrawOrder = %v, want %v", got, want)
	}
	// An in-range selection primitive swaps into slot 0; primitive 0 takes its
	// slot and is sorted with the rest.
	o.SelectionPrim = 2
	if got, want := o.DrawOrder(), []int{2, 6, 4, 1, 3, 5, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("selection 2: DrawOrder = %v, want %v", got, want)
	}
	// An out-of-range value leaves primitive 0 in slot 0.
	o.SelectionPrim = 40
	if got, want := o.DrawOrder(), []int{0, 6, 4, 1, 3, 5, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("selection 40: DrawOrder = %v, want %v", got, want)
	}
	if (&Object{}).DrawOrder() != nil {
		t.Error("empty object: want nil order")
	}
}

// The mean truncates toward zero and the sum wraps at 32 bits.
func TestMeanCornerY(t *testing.T) {
	o := &Object{Vertices: []Vertex{{Y: -7}, {Y: 0x7fffffff}, {Y: 1}}}
	if got := meanCornerY(o, &Primitive{VertexIndices: []int{0, 2}}); got != -3 {
		t.Errorf("mean(-7, 1) = %d, want -3", got)
	}
	if got := meanCornerY(o, &Primitive{VertexIndices: []int{1, 2}}); got != -0x40000000 {
		t.Errorf("wrapped mean = %d, want %d", got, -0x40000000)
	}
}
