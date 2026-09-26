// Package pal implements reading and writing of Total Annihilation .PAL
// palette files and of the palette lookup tables stored next to them.
//
// # Palettes
//
// A TA palette is 256 entries of 4 bytes: R, G, B and a fourth byte the game
// does not use (always 0 in Cavedog's files). The game loads a named palette
// from palettes/<name>.PAL as follows, and LoadNamed does the same:
//
//   - A file of 1,024 bytes or more: the first 1,024 bytes are the palette
//     and the rest is ignored. LoadFromBytes and LoadFromReader accept such
//     files.
//   - An empty or missing file: the palette is taken from
//     palettes/<name>.PCX instead, from the last 768 bytes of that file (see
//     FromPCX and the pcx package). TA 3.1c then wrote those colours back as
//     the .PAL and deleted PALETTE.ALP, .SHD and .LHT so that they were
//     rebuilt for the new colours.
//   - A file of 1 to 1,023 bytes is not a usable palette: the game reads
//     1,024 bytes regardless, past its end. The loaders return ErrShort and,
//     like the game, do not fall back to the PCX.
//
// Palettes decoded here report index 0 with alpha 0, the key colour sprites
// treat as transparent. The game draws terrain, minimaps and backdrops opaque,
// so use Palette.OpaqueColorModel for those.
//
// # Lookup tables
//
// PALETTE.ALP, PALETTE.SHD and PALETTE.LHT are index-to-index lookup tables
// built from PALETTE.PAL; each byte is a palette index, not a colour. See
// Table and TableKind:
//
//   - .ALP (65,536 bytes) is 256 rows of 256: entry [a][b] is the palette
//     index nearest the average of colours a and b, used to blend two pixels
//     half and half. It is symmetric and [a][a] is a.
//   - .SHD (8,192 bytes) is 32 rows of 256: row r maps each index to the
//     colour nearest that colour scaled by r × 0.06875: row 0 is black,
//     colours come back nearly unchanged around rows 14 and 15, and higher
//     rows brighten them.
//   - .LHT (8,192 bytes) is 32 rows of 256: row r maps each index to the
//     colour nearest that colour scaled by 1 + r/30, from unchanged (row 0) to
//     about twice as bright.
//
// The game uses a table file only when its size is exact; a missing table or
// one of any other size is rebuilt from the palette. TA: Kingdoms ships
// tables of the same sizes for each of its palettes.
package pal
