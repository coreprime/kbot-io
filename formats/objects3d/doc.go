// Package objects3d reads Total Annihilation 3DO model files and renders
// preview images of them. Nothing in the package writes 3DO files.
//
// # File layout
//
// A 3DO file is a tree of 52-byte object headers (pieces), the first at file
// offset 0. A header holds a version word, the vertex and primitive counts,
// the selection primitive index, the piece's offset from its parent, and the
// file offsets of its name, vertex array, primitive array, next sibling and
// first child. A vertex is three signed 16.16 fixed-point words (65536 units
// are one world pixel; Y is up). A primitive is a 32-byte record: a colour
// word, the count and offset of its unsigned 16-bit vertex indices, the offset
// of its texture name, two unknown words and the is_colored flag word. All
// words are little-endian and strings are NUL-terminated.
//
// # Loading
//
// LoadFromBytes and LoadFromReader decode the whole tree. The root may have
// siblings; the game loads them, with their children, after the root's
// subtree, and so does the loader: Model.RootSiblings lists them and
// Model.AllObjects holds every object in that preorder, which is the game's
// piece order. No retail TA 3.1c or TA: Kingdoms model has root siblings.
//
// An offset of 0 means "none" for names, texture names and sibling and child
// links, and an array whose count is 0 is never read. Anything else the file
// points at must lie inside it; the loader reports damage instead of
// returning a partial model. These are errors wrapping ErrMalformed:
//
//   - a root header shorter than 52 bytes, or any header, array or string
//     running past the end of the file (these also wrap io.ErrUnexpectedEOF);
//   - a negative count or offset, or a non-empty array at offset 0;
//   - a name or texture name with no NUL within 4096 bytes;
//   - an object header reached twice: a link cycle, or two links to one
//     subtree (no retail model shares objects);
//   - arrays that overlap so heavily that decoding them would exceed four
//     times the file size (plus 1 MiB). Counts are checked against the file
//     before anything is allocated.
//
// Two things are accepted as stored. A vertex index at or past its object's
// vertex count is kept: the game does not check it, and the renderer skips the
// primitive. The version word is kept in Object.VersionSignature and not
// checked; every retail model stores 1, and whether the game rejects other
// values has not been established.
//
// # How the game draws a model
//
// The loader keeps the stored words (Primitive.RawIsColored,
// Primitive.RawColorIndex, Object.SelectionPrim) and exposes the game's
// reading of them, so callers that draw models themselves can follow it:
//
//   - is_colored is a flag word, and only bit 0 (ColoredFlag) means
//     "coloured" (Primitive.IsColored). More than half of the primitives in
//     the retail models store values other than 0 and 1 in it: textured
//     primitives with bit 0 clear and untextured ones with bit 0 set.
//   - A coloured primitive is filled with the low byte of its colour word
//     (Primitive.ColorIndex).
//   - Primitive.Style gives the remaining rules: a texture name that does not
//     resolve (the game looks names up case-insensitively) fills the face
//     with palette index MissingTextureColor (0xd1); only four-corner
//     primitives are textured, corner 0 at the texture's top-left texel and
//     then clockwise; an uncoloured primitive with no texture is not drawn.
//   - Object.HiddenPrimitive is the primitive the game never draws: the
//     selection primitive when its index is in range, otherwise primitive 0
//     for any selection value but -1.
//   - Object.DrawOrder is the order primitives are drawn in. Pieces are
//     drawn from the last in AllObjects to the first (until a unit script
//     reorders them), without a depth buffer, so a later coplanar face
//     covers an earlier one.
//   - A face is drawn only from its front: the side its corner order faces by
//     the right-hand rule, in file coordinates.
//   - The game's view mirrors file X: file +X appears on the left when +Z
//     faces the viewer.
//   - Root siblings and their descendants are drawn at their own vertex
//     coordinates: the game's piece transform starts at the root and applies
//     no piece offsets to them.
//
// These are TA 3.1c's rules. TA: Kingdoms models use the same file format but
// also carry textured triangles, and their textures are keyed; the renderer's
// TexturePolygons and KeyedTextures options cover both.
//
// # Rendering
//
// Model.RenderImage, RenderPNG and RenderSpinAPNG rasterise a model for
// previews, following the rules above: X is mirrored, the hidden primitive is
// skipped, faces are styled with Primitive.Style (textures and palette colours
// come from a Material), and with CullBackFaces (on in DefaultRenderOptions)
// back faces are culled. The camera is an orthographic rotation rather than
// the game's fixed oblique projection, faces are lit with a simple directional
// light, and visibility uses a depth buffer; triangles are emitted in the
// game's draw order and a later face wins a depth tie, matching the game's
// painter's order for coplanar faces. HideBaseplates optionally hides flat
// coloured quads at the base of the model, which the game draws.
//
// FillModel adds synthetic faces that close holes the artists left open. The
// game never draws them; the renderer textures them whatever their corner
// count.
package objects3d
