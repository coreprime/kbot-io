package objects3d

import (
	"image"
	"image/color"
	"reflect"
	"testing"
)

func countOpaque(img *image.RGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] == 0xff {
			n++
		}
	}
	return n
}

func TestRenderImageCube(t *testing.T) {
	root := colouredCube(1000)
	m := &Model{Root: root, AllObjects: []*Object{root}}

	opts := DefaultRenderOptions()
	opts.UnitsPerPixel = 1 // fill the frame for the test
	img := m.RenderImage(opts)
	if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatalf("unexpected size %v", img.Bounds())
	}
	// A cube filling ~76% of the frame should cover a large chunk of pixels.
	if opaque := countOpaque(img); opaque < 2000 {
		t.Fatalf("expected the cube to fill many pixels, got %d opaque", opaque)
	}

	b, err := m.RenderPNG(opts)
	if err != nil || len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("RenderPNG bad output: err=%v len=%d", err, len(b))
	}
}

func TestRenderEmptyModel(t *testing.T) {
	img := (&Model{}).RenderImage(DefaultRenderOptions())
	if img == nil || img.Bounds().Dx() != 128 {
		t.Fatal("empty model should still return a blank 128px image")
	}
}

func TestRenderSpinAPNG(t *testing.T) {
	root := colouredCube(1000)
	m := &Model{Root: root, AllObjects: []*Object{root}}
	so := DefaultRenderOptions()
	so.UnitsPerPixel = 1
	b, err := m.RenderSpinAPNG(so, 12, 90)
	if err != nil {
		t.Fatalf("RenderSpinAPNG: %v", err)
	}
	// APNG starts with the PNG signature and must carry an acTL chunk.
	if len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("not a PNG/APNG (len=%d)", len(b))
	}
	if !bytesContains(b, []byte("acTL")) || !bytesContains(b, []byte("fcTL")) {
		t.Fatalf("missing APNG animation chunks")
	}
}

func bytesContains(h, n []byte) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return true
		}
	}
	return false
}

// Palette used by testMaterial: pure primaries survive the face shading.
var (
	red    = color.RGBA{0xff, 0, 0, 0xff}
	green  = color.RGBA{0, 0xff, 0, 0xff}
	blue   = color.RGBA{0, 0, 0xff, 0xff}
	white  = color.RGBA{0xff, 0xff, 0xff, 0xff}
	purple = color.RGBA{0xff, 0, 0xff, 0xff} // palette MissingTextureColor
)

// testMaterial resolves palette indices 1 (red), 2 (green), 3 (blue) and
// MissingTextureColor (purple), and textures by exact name.
type testMaterial struct {
	textures  map[string]*image.RGBA
	requested []int
}

func (m *testMaterial) Texture(name string) (*image.RGBA, bool) {
	t, ok := m.textures[name]
	return t, ok
}

func (m *testMaterial) PaletteColor(i int) (color.RGBA, bool) {
	m.requested = append(m.requested, i)
	switch i {
	case 1:
		return red, true
	case 2:
		return green, true
	case 3:
		return blue, true
	case MissingTextureColor:
		return purple, true
	}
	return color.RGBA{}, false
}

// topDown looks straight down the Y axis with file +Z at the bottom of the
// image, the orientation of the game's view.
func topDown(mat Material) RenderOptions {
	o := DefaultRenderOptions()
	o.AzimuthDeg, o.ElevationDeg = 0, 90
	o.Width, o.Height = 64, 64
	o.FitToFrame = true
	o.Material = mat
	return o
}

// upQuad returns four vertices of a horizontal square centred at (x, y, z),
// wound so the face points up (+Y). Corner 0 has the largest X and smallest
// Z, which the game shows at the top-left.
func upQuad(x, y, z, h int32) []Vertex {
	return []Vertex{{x + h, y, z - h}, {x - h, y, z - h}, {x - h, y, z + h}, {x + h, y, z + h}}
}

// singleObject builds a model of one object from quads given as vertex sets.
func singleObject(prims []Primitive, quads ...[]Vertex) *Model {
	o := &Object{SelectionPrim: -1}
	for qi, q := range quads {
		o.Vertices = append(o.Vertices, q...)
		base := 4 * qi
		prims[qi].VertexIndices = []int{base, base + 1, base + 2, base + 3}
	}
	o.Primitives = prims
	return &Model{Root: o, AllObjects: []*Object{o}}
}

// colourAt classifies a pixel by its dominant channels.
func colourAt(img *image.RGBA, x, y int) string {
	c := img.RGBAAt(x, y)
	if c.A == 0 {
		return "none"
	}
	hi := func(v uint8) bool { return v > 0x60 }
	switch {
	case hi(c.R) && hi(c.G) && hi(c.B):
		return "white"
	case hi(c.R) && hi(c.B):
		return "purple"
	case hi(c.R):
		return "red"
	case hi(c.G):
		return "green"
	case hi(c.B):
		return "blue"
	}
	return "other"
}

// meanX returns the mean x of pixels classified as colour, and their count.
func meanX(img *image.RGBA, colour string) (float64, int) {
	sum, n := 0, 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if colourAt(img, x, y) == colour {
				sum += x
				n++
			}
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(sum) / float64(n), n
}

// The game shows file +X on the left when +Z faces the viewer.
func TestRenderMirrorsX(t *testing.T) {
	m := singleObject(
		[]Primitive{{IsColored: true, ColorIndex: 1}, {IsColored: true, ColorIndex: 2}},
		upQuad(0, 0, 0, 100), upQuad(300, 0, 0, 50),
	)
	img := m.RenderImage(topDown(&testMaterial{}))
	rx, rn := meanX(img, "red")
	gx, gn := meanX(img, "green")
	if rn == 0 || gn == 0 {
		t.Fatalf("red %d px, green %d px", rn, gn)
	}
	if gx >= rx {
		t.Errorf("the +X quad is drawn right of the origin quad (green x %.1f, red x %.1f)", gx, rx)
	}
}

// Quad corner 0 takes the texture's top-left texel, corner 2 its
// bottom-right, and the texture appears upright in the game's view.
func TestRenderQuadTextureOrientation(t *testing.T) {
	tex := image.NewRGBA(image.Rect(0, 0, 2, 2))
	tex.SetRGBA(0, 0, red)
	tex.SetRGBA(1, 0, green)
	tex.SetRGBA(0, 1, blue)
	tex.SetRGBA(1, 1, white)
	mat := &testMaterial{textures: map[string]*image.RGBA{"tex": tex}}
	m := singleObject([]Primitive{{TextureName: "tex"}}, upQuad(0, 0, 0, 100))
	img := m.RenderImage(topDown(mat))
	for _, c := range []struct {
		x, y int
		want string
	}{{22, 22, "red"}, {42, 22, "green"}, {22, 42, "blue"}, {42, 42, "white"}} {
		if got := colourAt(img, c.x, c.y); got != c.want {
			t.Errorf("pixel (%d,%d) = %s, want %s", c.x, c.y, got, c.want)
		}
	}
}

// Back faces are culled by the game's winding when CullBackFaces is set.
func TestRenderCullBackFaces(t *testing.T) {
	down := upQuad(0, 0, 0, 100)
	down[1], down[3] = down[3], down[1] // reverse the winding: faces -Y
	m := singleObject([]Primitive{{IsColored: true, ColorIndex: 1}}, down)
	opts := topDown(&testMaterial{})
	if !opts.CullBackFaces {
		t.Fatal("DefaultRenderOptions should cull back faces")
	}
	if n := countOpaque(m.RenderImage(opts)); n != 0 {
		t.Errorf("back face drew %d pixels with culling on", n)
	}
	opts.CullBackFaces = false
	if n := countOpaque(m.RenderImage(opts)); n == 0 {
		t.Error("back face drew nothing with culling off")
	}
	up := singleObject([]Primitive{{IsColored: true, ColorIndex: 1}}, upQuad(0, 0, 0, 100))
	opts.CullBackFaces = true
	if n := countOpaque(up.RenderImage(opts)); n == 0 {
		t.Error("front face drew nothing with culling on")
	}
}

// triangleModel is one up-facing triangle with the given primitive fields.
func triangleModel(p Primitive) *Model {
	q := upQuad(0, 0, 0, 100)
	p.VertexIndices = []int{0, 1, 2}
	o := &Object{Vertices: q[:3], Primitives: []Primitive{p}, SelectionPrim: -1}
	return &Model{Root: o, AllObjects: []*Object{o}}
}

// Only four-corner primitives are textured; a textured triangle is not drawn
// unless TexturePolygons is set.
func TestRenderTexturesQuadsOnly(t *testing.T) {
	tex := image.NewRGBA(image.Rect(0, 0, 1, 1))
	tex.SetRGBA(0, 0, blue)
	mat := &testMaterial{textures: map[string]*image.RGBA{"tex": tex}}
	m := triangleModel(Primitive{TextureName: "tex"})
	opts := topDown(mat)
	if n := countOpaque(m.RenderImage(opts)); n != 0 {
		t.Errorf("textured triangle drew %d pixels", n)
	}
	opts.TexturePolygons = true
	img := m.RenderImage(opts)
	if _, n := meanX(img, "blue"); n == 0 {
		t.Error("TexturePolygons: textured triangle not drawn")
	}
	// A synthetic triangle (from FillModel) is textured regardless.
	opts.TexturePolygons = false
	m = triangleModel(Primitive{TextureName: "tex", Synthetic: true})
	if _, n := meanX(m.RenderImage(opts), "blue"); n == 0 {
		t.Error("synthetic textured triangle not drawn")
	}
}

// A texture that does not resolve fills the face with palette 0xd1, whatever
// its corner count; a face with neither texture nor colour is not drawn.
func TestRenderMissingAndUntextured(t *testing.T) {
	mat := &testMaterial{}
	opts := topDown(mat)

	m := singleObject([]Primitive{{TextureName: "nosuch"}}, upQuad(0, 0, 0, 100))
	if _, n := meanX(m.RenderImage(opts), "purple"); n == 0 {
		t.Error("missing-texture quad not filled with MissingTextureColor")
	}
	if _, n := meanX(triangleModel(Primitive{TextureName: "nosuch"}).RenderImage(opts), "purple"); n == 0 {
		t.Error("missing-texture triangle not filled with MissingTextureColor")
	}

	m = singleObject([]Primitive{{}}, upQuad(0, 0, 0, 100))
	if n := countOpaque(m.RenderImage(opts)); n != 0 {
		t.Errorf("untextured uncoloured quad drew %d pixels", n)
	}
	for _, i := range mat.requested {
		if i < 0 || i > 0xff {
			t.Errorf("palette index %d requested", i)
		}
	}
}

// The game never draws slot 0 when the selection value is anything but -1.
func TestRenderSelectionRule(t *testing.T) {
	build := func(sel int32) *Model {
		m := singleObject(
			[]Primitive{{IsColored: true, ColorIndex: 1}, {IsColored: true, ColorIndex: 2}},
			upQuad(200, 0, 0, 100), upQuad(-200, 0, 0, 100),
		)
		m.Root.SelectionPrim = sel
		return m
	}
	opts := topDown(&testMaterial{})
	for _, c := range []struct {
		sel        int32
		red, green bool
	}{{-1, true, true}, {0, false, true}, {1, true, false}, {7, false, true}, {-3, false, true}} {
		img := build(c.sel).RenderImage(opts)
		_, rn := meanX(img, "red")
		_, gn := meanX(img, "green")
		if (rn > 0) != c.red || (gn > 0) != c.green {
			t.Errorf("selection %d: red drawn %v, green drawn %v; want %v, %v", c.sel, rn > 0, gn > 0, c.red, c.green)
		}
	}
}

// The flat-base heuristic is opt-in, and it range-checks before reading
// vertices.
func TestRenderBaseplateHeuristicOptIn(t *testing.T) {
	top := upQuad(300, 1000, 0, 100)
	base := upQuad(-300, 0, 0, 100)
	m := singleObject([]Primitive{{IsColored: true, ColorIndex: 1}, {IsColored: true, ColorIndex: 2}}, top, base)
	opts := topDown(&testMaterial{})
	if _, n := meanX(m.RenderImage(opts), "green"); n == 0 {
		t.Error("coloured base quad hidden by default")
	}
	opts.HideBaseplates = true
	if _, n := meanX(m.RenderImage(opts), "green"); n != 0 {
		t.Error("HideBaseplates: base quad still drawn")
	}
	// A coloured quad with an out-of-range index must not panic.
	m.Root.Primitives = append(m.Root.Primitives, Primitive{IsColored: true, ColorIndex: 3, VertexIndices: []int{0, 1, 2, 99}})
	_ = m.RenderImage(opts)
}

// TA samples model textures without a key; keyed sampling is opt-in.
func TestRenderKeyedTextures(t *testing.T) {
	tex := image.NewRGBA(image.Rect(0, 0, 1, 1))
	copy(tex.Pix, []byte{0xff, 0, 0, 0}) // red with alpha 0
	mat := &testMaterial{textures: map[string]*image.RGBA{"tex": tex}}
	m := singleObject([]Primitive{{TextureName: "tex"}}, upQuad(0, 0, 0, 100))
	opts := topDown(mat)
	if _, n := meanX(m.RenderImage(opts), "red"); n == 0 {
		t.Error("unkeyed sampling skipped alpha-0 texels")
	}
	opts.KeyedTextures = true
	if n := countOpaque(m.RenderImage(opts)); n != 0 {
		t.Errorf("keyed sampling drew %d alpha-0 texels", n)
	}
}

// Coplanar faces resolve as in the game's painter's order: within a piece the
// later primitive wins; across pieces the earlier piece (drawn later) wins.
func TestRenderCoplanarTies(t *testing.T) {
	opts := topDown(&testMaterial{})
	q := upQuad(0, 0, 0, 100)
	m := singleObject([]Primitive{{IsColored: true, ColorIndex: 1}, {IsColored: true, ColorIndex: 2}}, q, q)
	if got := colourAt(m.RenderImage(opts), 32, 32); got != "green" {
		t.Errorf("same piece: centre is %s, want green (the later primitive)", got)
	}

	child := &Object{Vertices: q, SelectionPrim: -1,
		Primitives: []Primitive{{IsColored: true, ColorIndex: 2, VertexIndices: []int{0, 1, 2, 3}}}}
	root := &Object{Vertices: q, SelectionPrim: -1, Children: []*Object{child},
		Primitives: []Primitive{{IsColored: true, ColorIndex: 1, VertexIndices: []int{0, 1, 2, 3}}}}
	m = &Model{Root: root, AllObjects: []*Object{root, child}}
	if got := colourAt(m.RenderImage(opts), 32, 32); got != "red" {
		t.Errorf("two pieces: centre is %s, want red (the root, drawn last)", got)
	}
}

// Root siblings are drawn, at their own vertex coordinates: their offsets
// (and their children's) are not applied.
func TestRenderRootSiblings(t *testing.T) {
	quad := []int{0, 1, 2, 3}
	grandchild := &Object{XFromParent: 5000, Vertices: upQuad(0, 20, 0, 50), SelectionPrim: -1,
		Primitives: []Primitive{{IsColored: true, ColorIndex: 3, VertexIndices: quad}}}
	sibling := &Object{XFromParent: 100000, YFromParent: 100000, Vertices: upQuad(0, 10, 0, 150), SelectionPrim: -1,
		Children:   []*Object{grandchild},
		Primitives: []Primitive{{IsColored: true, ColorIndex: 2, VertexIndices: quad}}}
	rootChild := &Object{XFromParent: 400, SelectionPrim: -1}
	root := &Object{XFromParent: 7, Vertices: upQuad(0, 0, 0, 100), SelectionPrim: -1,
		Children:   []*Object{rootChild},
		Primitives: []Primitive{{IsColored: true, ColorIndex: 1, VertexIndices: quad}}}
	m := &Model{Root: root, RootSiblings: []*Object{sibling}, AllObjects: []*Object{root, rootChild, sibling, grandchild}}

	placed := m.placedPieces()
	want := []struct {
		obj *Object
		x   float64
	}{{root, 7}, {rootChild, 407}, {sibling, 0}, {grandchild, 0}}
	if len(placed) != len(want) {
		t.Fatalf("placed %d pieces, want %d", len(placed), len(want))
	}
	for i, w := range want {
		if placed[i].obj != w.obj || placed[i].origin.X != w.x || (w.x == 0 && placed[i].origin.Y != 0) {
			t.Errorf("piece %d: origin %+v, want x %v", i, placed[i].origin, w.x)
		}
	}

	img := m.RenderImage(topDown(&testMaterial{}))
	if _, n := meanX(img, "red"); n != 0 {
		t.Error("root quad visible: the sibling should cover it at the same place")
	}
	if got := colourAt(img, 32, 32); got != "blue" {
		t.Errorf("centre is %s, want blue (the sibling's child, highest)", got)
	}
	if _, n := meanX(img, "green"); n == 0 {
		t.Error("root sibling not drawn")
	}
}

// A hand-built tree with a child cycle renders instead of looping forever.
func TestRenderCycleSafe(t *testing.T) {
	root := colouredCube(100)
	child := &Object{SelectionPrim: -1}
	root.Children = []*Object{child}
	child.Children = []*Object{root}
	m := &Model{Root: root, AllObjects: []*Object{root, child}}
	if n := countOpaque(m.RenderImage(topDown(nil))); n == 0 {
		t.Error("nothing drawn")
	}
}

// KingdomsRenderOptions differs from DefaultRenderOptions only in turning on
// keyed sampling and textured polygons, so TA: Kingdoms previews draw their
// textured triangles and skip keyed texels.
func TestKingdomsRenderOptions(t *testing.T) {
	k, d := KingdomsRenderOptions(), DefaultRenderOptions()
	if !k.KeyedTextures || !k.TexturePolygons {
		t.Fatalf("KeyedTextures = %v, TexturePolygons = %v, want both on", k.KeyedTextures, k.TexturePolygons)
	}
	k.KeyedTextures, k.TexturePolygons = false, false
	if !reflect.DeepEqual(k, d) {
		t.Errorf("other fields differ from DefaultRenderOptions:\n%+v\n%+v", k, d)
	}

	tex := image.NewRGBA(image.Rect(0, 0, 2, 1))
	tex.SetRGBA(0, 0, blue)
	copy(tex.Pix[4:], []byte{0xff, 0, 0, 0}) // red with alpha 0
	mat := &testMaterial{textures: map[string]*image.RGBA{"tex": tex}}
	opts := KingdomsRenderOptions()
	opts.AzimuthDeg, opts.ElevationDeg = 0, 90
	opts.Width, opts.Height = 64, 64
	opts.FitToFrame = true
	opts.Material = mat
	img := triangleModel(Primitive{TextureName: "tex"}).RenderImage(opts)
	if _, n := meanX(img, "blue"); n == 0 {
		t.Error("textured triangle not drawn")
	}
	if _, n := meanX(img, "red"); n != 0 {
		t.Errorf("%d keyed texels drawn", n)
	}
}
