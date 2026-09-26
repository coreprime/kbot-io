// Package pcx reads ZSoft PCX images, the format Total Annihilation uses for
// backdrops, screenshots and palette carriers.
//
// [Reader.Decode] follows the PCX specification: it honours BytesPerLine,
// decodes 8-bit single-plane images as paletted and 24-bit three-plane images
// as RGBA, and takes the colour map from the last 768 bytes only when a 0x0C
// marker precedes it (and follows the 128-byte header). A BytesPerLine below
// the width, which the specification does not allow, is read as the width.
//
// # Palettes and transparency
//
// Images decoded by this package always carry fully opaque palettes, whatever
// the palette source. When a standard-mode 8-bit file has no 0x0C marker, the
// built-in TA palette (palettes.DefaultPalette) is used. The game draws
// backdrops opaque too. [Reader.EmbeddedPalette] returns the marked colour map
// as a *gaf.Palette for rendering sprites, with index 0 following the gaf
// package convention.
//
// # Truncated data
//
// When the pixel data ends before the last row, 8-bit decoding still
// succeeds and leaves the missing pixels at index 0; the game also keeps
// going, drawing from the last byte it read. [Reader.Truncated] reports
// whether that happened, so callers can tell a complete image from a partial
// one. 24-bit decoding fails with an error wrapping [ErrTruncated].
//
// # Limits
//
// Images above 64 Mi pixels are refused before any pixel buffer is
// allocated. The largest retail image is 640x480.
package pcx
