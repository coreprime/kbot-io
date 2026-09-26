// Package pcx reads ZSoft PCX images, the format Total Annihilation uses for
// backdrops, screenshots and palette carriers.
//
// # Decode modes
//
// [Reader.Decode] follows the PCX specification (ModeStandard): it honours
// BytesPerLine, decodes 8-bit single-plane images as paletted and 24-bit
// three-plane images as RGBA, and takes the colour map from the last 768
// bytes only when a 0x0C marker precedes it (and follows the 128-byte
// header). A BytesPerLine below the width, which the specification does not
// allow, is read as the width. This is how image editors read the files, and
// it is the right choice for TA: Kingdoms data, which ships many one-pixel
// palette carriers whose BytesPerLine is padded to 2.
//
// [Reader.DecodeGame] (ModeGame) decodes a file the way Total Annihilation
// 3.1c does, so previews show what the game will draw:
//
//   - The file must start with the manufacturer byte 0x0A and version 5; the
//     game refuses any other version.
//   - Width and height are XMax-XMin+1 and YMax-YMin+1 computed in full
//     integers, so a width of 65,536 is possible; a maximum below its minimum
//     is refused.
//   - Encoding, BitsPerPixel, NumPlanes and BytesPerLine are ignored. Every
//     file is run-length decoded as 8-bit single-plane data, each row exactly
//     width pixels wide, straight after the 128-byte header. Padding declared
//     through a BytesPerLine larger than the width therefore flows into the
//     next row and skews the image, and a 24-bit file shows as garbage.
//   - A byte with its top two bits set is a run of (byte & 0x3F) copies of
//     the next byte. A run that passes the end of the row is clipped and the
//     rest of it is dropped; a zero-length run consumes its value byte and
//     draws nothing.
//   - The colour map is always the last 768 bytes of the file, whether or not
//     a 0x0C marker precedes it; a file shorter than 896 bytes (header plus
//     colour map) is refused.
//   - Backdrops are drawn opaque: palette index 0 is an ordinary colour.
//
// [Reader.Compat] lists every way a file departs from what the game expects,
// so tools can warn before an asset ships.
//
// # Palettes and transparency
//
// Images decoded by this package always carry fully opaque palettes, whatever
// the palette source. When a standard-mode 8-bit file has no 0x0C marker, the
// built-in TA palette (palettes.DefaultPalette) is used. [Reader.GamePalette]
// returns the colours the game uses (the last 768 bytes), and
// [Reader.EmbeddedPalette] returns the marked colour map as a *gaf.Palette
// for rendering sprites, with index 0 following the gaf package convention.
//
// # Truncated data
//
// When the pixel data ends before the last row, 8-bit decoding (both modes)
// still succeeds and leaves the missing pixels at index 0; the game also keeps
// going, drawing from the last byte it read. [Reader.Truncated] reports
// whether that happened, so callers can tell a complete image from a partial
// one. 24-bit decoding fails with an error wrapping [ErrTruncated].
//
// # Limits
//
// Images above 64 Mi pixels are refused before any pixel buffer is
// allocated. The largest retail image is 640x480.
package pcx
