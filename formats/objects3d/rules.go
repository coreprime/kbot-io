package objects3d

import "sort"

// FaceStyle is how the game draws a primitive.
type FaceStyle int

const (
	// FaceHidden means the game draws nothing for the primitive.
	FaceHidden FaceStyle = iota
	// FaceFilled means the game fills the primitive with one palette colour.
	FaceFilled
	// FaceTextured means the game maps the primitive's texture onto its four
	// corners: corner 0 takes the texture's top-left texel, corner 1 the
	// top-right, corner 2 the bottom-right and corner 3 the bottom-left.
	FaceTextured
)

// String returns the style's name.
func (s FaceStyle) String() string {
	switch s {
	case FaceHidden:
		return "hidden"
	case FaceFilled:
		return "filled"
	case FaceTextured:
		return "textured"
	}
	return "unknown"
}

// Style reports how TA 3.1c draws the primitive, and the palette index of a
// filled one. textureFound says whether the host resolved TextureName to a
// texture (the game looks names up case-insensitively); it is ignored for a
// primitive with no texture name.
//
// A primitive whose texture name does not resolve is filled with
// MissingTextureColor, whether or not it is coloured. Otherwise a coloured
// primitive (IsColored) is filled with ColorIndex, whatever its corner count.
// An uncoloured primitive with a resolved texture is textured only when it has
// exactly four corners; with any other count, or with no texture at all, it is
// not drawn. A primitive with fewer than three corners covers no pixels and is
// reported hidden. Style does not check vertex indices, and the selection
// primitive rule (Object.HiddenPrimitive) is separate. TA: Kingdoms models
// also carry textured triangles; RenderOptions.TexturePolygons textures them.
func (p Primitive) Style(textureFound bool) (FaceStyle, uint8) {
	if len(p.VertexIndices) < 3 {
		return FaceHidden, 0
	}
	if p.TextureName != "" && !textureFound {
		return FaceFilled, MissingTextureColor
	}
	if p.IsColored {
		return FaceFilled, uint8(p.ColorIndex)
	}
	if p.TextureName != "" && len(p.VertexIndices) == 4 {
		return FaceTextured, 0
	}
	return FaceHidden, 0
}

// HiddenPrimitive returns the index of the object's primitive that is not
// drawn, or -1 when every primitive is drawn.
//
// With SelectionPrim -1 or no primitives nothing is hidden. A selection index
// inside the primitive list hides that primitive: TA 3.1c moves it into slot
// 0 and never draws slot 0. For any other value (an index at or past the end
// of the list, or a negative value other than -1) HiddenPrimitive hides
// primitive 0, the primitive left in slot 0; whether the game does the same
// for such values has not been established. In the retail TA and TA: Kingdoms
// models such values occur only on objects with no primitives, so no retail
// model is affected either way.
func (o *Object) HiddenPrimitive() int {
	n := len(o.Primitives)
	switch {
	case o.SelectionPrim == -1 || n == 0:
		return -1
	case o.SelectionPrim >= 0 && int(o.SelectionPrim) < n:
		return int(o.SelectionPrim)
	default:
		return 0
	}
}

// DrawOrder returns the object's primitive indices in the order the game
// draws them, or nil for an object with no primitives.
//
// The order is fixed when the model loads. An in-range selection primitive
// (see HiddenPrimitive) is swapped with primitive 0; the primitives after
// slot 0 are then ordered by the mean Y of their corners, lowest first,
// keeping their relative order on ties. The mean is the 32-bit wrapping sum of
// the corners' Y divided by the corner count, truncated toward zero; a corner
// whose index is past the vertex list adds nothing, and a primitive with no
// corners has mean 0. Whenever HiddenPrimitive is not -1, it is the first
// entry.
func (o *Object) DrawOrder() []int {
	n := len(o.Primitives)
	if n == 0 {
		return nil
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	if sel := o.SelectionPrim; sel != -1 && sel >= 0 && int(sel) < n {
		order[0], order[sel] = order[sel], order[0]
	}
	means := make([]int32, n)
	for i := range o.Primitives {
		means[i] = meanCornerY(o, &o.Primitives[i])
	}
	rest := order[1:]
	sort.SliceStable(rest, func(a, b int) bool { return means[rest[a]] < means[rest[b]] })
	return order
}

// meanCornerY is the draw-order sort key of a primitive.
func meanCornerY(o *Object, p *Primitive) int32 {
	count := len(p.VertexIndices)
	if count == 0 {
		return 0
	}
	var sum uint32
	for _, i := range p.VertexIndices {
		if i >= 0 && i < len(o.Vertices) {
			sum += uint32(o.Vertices[i].Y)
		}
	}
	return int32(sum) / int32(count)
}
