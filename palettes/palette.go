// Package palettes embeds the stock palettes as raw .PAL bytes: 256 entries
// of four bytes (R, G, B and a byte the game does not use), 1,024 bytes in
// all. Parse them with the pal package (pal.LoadFromBytes) or
// gaf.LoadPaletteFromBytes.
package palettes

import (
	_ "embed"
)

// DefaultPalette contains the embedded Total Annihilation game palette,
// palettes/PALETTE.PAL (1,024 bytes).
//
//go:embed PALETTE.PAL
var DefaultPalette []byte

// TA: Kingdoms renders terrain (and the paletted minimap baked into each
// .tnt) with a per-kingdom texture palette, selected by the map's `kingdom=`
// field. Each is a 1,024-byte .PAL in the same layout as DefaultPalette.

//go:embed TAK_ARAMON.PAL
var takAramon []byte

//go:embed TAK_TAROS.PAL
var takTaros []byte

//go:embed TAK_VERUNA.PAL
var takVeruna []byte

//go:embed TAK_ZHON.PAL
var takZhon []byte

//go:embed TAK_CREON.PAL
var takCreon []byte

// TAKPalettes maps a TA: Kingdoms kingdom name (lower-case) to the raw bytes
// of its terrain/minimap palette.
var TAKPalettes = map[string][]byte{
	"aramon": takAramon,
	"taros":  takTaros,
	"veruna": takVeruna,
	"zhon":   takZhon,
	"creon":  takCreon,
}
