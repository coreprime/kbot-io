// Package tnt implements reading of Total Annihilation TNT map files.
package tnt

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math/bits"
	"strings"

	"github.com/coreprime/kbot-io/formats/tnt/tak"
)

// TNT IDVersion words. TA writes 0x2000 and also loads the older 0x1020
// layout; TA: Kingdoms reuses the TNT container with a bumped version word and
// a different field layout that TA does not load.
const (
	VersionTA     = 8192  // 0x2000 — Total Annihilation
	VersionLegacy = 4128  // 0x1020 — older Total Annihilation layout (read only)
	VersionTAK    = 16384 // 0x4000 — Total Annihilation: Kingdoms
)

// Header is the 64-byte TNT file header.
//
// The field names describe the Total Annihilation (0x2000) layout. The legacy
// 0x1020 layout matches it except that its minimap pointer and presence
// flags live in Pad3 (0x38) and Pad4 (0x3c). TA: Kingdoms (0x4000) keeps the
// same 64-byte size but moves several fields: notably its minimap pointer
// lands in the Unknown1 slot (offset 0x2c).
type Header struct {
	IDVersion   uint32 // 0x2000 (TA) or 0x4000 (TA:K)
	Width       uint32 // TA: width in 16px attribute cells (tiles = Width/2). TA:K: width in 16px DataUnits.
	Height      uint32 // TA: height in 16px attribute cells (tiles = Height/2). TA:K: height in 16px DataUnits.
	PTRMapData  uint32 // TA: tile index array. TA:K: sea level.
	PTRMapAttr  uint32 // TA: attribute array. TA:K: heightmap (Width×Height bytes).
	PTRTileGfx  uint32 // TA: tile graphics. TA:K: attribute/feature grid (Width×Height uint16).
	Tiles       uint32 // TA: number of unique tiles. TA:K: feature name table offset.
	TileAnims   uint32 // TA: number of feature entries. TA:K: feature count.
	PTRTileAnim uint32 // TA: feature structures. TA:K: terrain-name table (guW×guH uint32).
	SeaLevel    uint32 // TA: sea level. TA:K: U-mapping table (guW×guH bytes).
	PTRMinimap  uint32 // TA: minimap (252×252). TA:K: V-mapping table (guW×guH bytes).
	Unknown1    uint32 // TA: minimap presence flags (see MinimapPresent). TA:K: minimap pointer (126×126 block).
	Pad1        uint32
	Pad2        uint32
	Pad3        uint32 // 0x1020: minimap pointer.
	Pad4        uint32 // 0x1020: minimap presence flags.
}

// The TA: Kingdoms meaning of each repurposed header slot (sea level, heightmap,
// feature grid + name table, terrain-name table, U/V maps, minimap) lives in the
// formats/tnt/tak subpackage, which owns the 0x4000 read/write variance.

// takNoFeature is the threshold at or above which a feature-grid cell holds a
// sentinel (e.g. 0xFFFF, 0xFFFB) rather than a feature index.
const takNoFeature = 0xFF00

// Feature words stored in TileAttr.Feature. A word below the feature-table
// count (Header.TileAnims) and below FeatureSentinelFloor places that table
// entry on the cell; every other word places nothing.
const (
	// FeatureNone marks a cell with no feature.
	FeatureNone uint16 = 0xFFFF
	// FeatureVoid marks a void cell: the game makes it impassable and
	// unbuildable although no feature stands there.
	FeatureVoid uint16 = 0xFFFC
	// FeatureSentinelFloor is the lowest sentinel word. Words at or above it
	// are never feature-table indices, whatever the table size, so a
	// feature table must hold fewer than FeatureSentinelFloor entries.
	FeatureSentinelFloor uint16 = 0xFFFB
)

// TileAttr is the per-cell attribute (4 bytes).
// There is one attribute per 16×16 pixel cell — 4 per 32×32 tile.
type TileAttr struct {
	Height  uint8  // Elevation at this cell
	Feature uint16 // Feature-table index or sentinel word (FeatureNone, FeatureVoid, ...)
	Pad     uint8
}

// LegacyTileAttr is one raw 8-byte attribute record of a 0x1020 map. The
// game reads the height at +0, an 8-bit feature index at +2 and a per-cell
// byte at +6 that 0x2000 maps do not carry; the other bytes are unused.
type LegacyTileAttr [8]byte

// legacyFeatureSentinelFloor is the lowest sentinel value of a 0x1020 map's
// 8-bit feature byte; values at or above it place no feature.
const legacyFeatureSentinelFloor = 0xFC

// Map is a parsed TNT file.
type Map struct {
	Header Header
	IsTAK  bool // true when the file is a TA: Kingdoms TNT (IDVersion 0x4000)

	// LegacyAttr holds the raw 8-byte attribute records of a 0x1020 map
	// (nil for every other version). TileAttr carries the same cells in
	// the 0x2000 form: the height, and the feature byte as a feature
	// index, or FeatureNone for a byte at or above 0xFC.
	LegacyAttr []LegacyTileAttr

	TileW    int        // Tile grid width (Header.Width / 2)
	TileH    int        // Tile grid height (Header.Height / 2)
	AttrW    int        // Attribute grid width (Header.Width)
	AttrH    int        // Attribute grid height (Header.Height)
	TileMap  []uint16   // TileW × TileH tile indices
	TileAttr []TileAttr // AttrW × AttrH attributes (16px resolution)
	Tiles    [][]byte   // Tile graphics, each 1024 bytes
	Minimap  []byte     // Minimap palette indices (or nil)
	MinimapW int
	MinimapH int

	// TA: Kingdoms data. TA:K does not store a TA-style tile mosaic. Terrain is
	// texture-mapped: a grid of 32px Graphic Units, each naming a JPG texture
	// plus a U/V offset into it. A separate heightmap and feature grid sit at
	// DataUnit (16px) resolution. These are populated only when IsTAK is true.
	TAKW            int      // Width in 16px DataUnits (Header.Width)
	TAKH            int      // Height in 16px DataUnits (Header.Height)
	TAKGUW          int      // Graphic-Unit grid width  (TAKW/2; 32px units)
	TAKGUH          int      // Graphic-Unit grid height (TAKH/2; 32px units)
	TAKHeight       []byte   // TAKW×TAKH heightmap (one byte per DataUnit)
	TAKFeatureGrid  []uint16 // TAKW×TAKH feature indices (>=takNoFeature = none)
	TAKTerrainNames []uint32 // TAKGUW×TAKGUH terrain texture names ("%08X.JPG")
	TAKUMap         []byte   // TAKGUW×TAKGUH texture column offsets (32px units)
	TAKVMap         []byte   // TAKGUW×TAKGUH texture row offsets (32px units)
	// TAKFeatureTableRaw is the feature-name table captured verbatim (its
	// internal layout isn't modelled). SaveTAK writes it back unchanged so a
	// map with features round-trips; the entry count lives in Header.TileAnims.
	TAKFeatureTableRaw []byte

	// MapDataPad preserves any padding bytes between the end of the
	// tile-index array and the start of the attribute array.  Cavedog's
	// authoring tools pad the mapdata block to a 16-byte boundary (with
	// scratch memory), so we capture it verbatim for byte-perfect
	// round-trip.  May be empty.
	MapDataPad []byte
}

// LoadFromReader parses a TNT file.
//
// Every section is checked against the length of r before it is allocated or
// read, so a header that claims more data than the file holds fails with an
// error instead of a huge allocation. A tile map, attribute block, tile set or
// feature table that runs past the end of the file is an error; a section with
// no bytes (a zero count) is never checked, because nothing reads its pointer.
// A missing or malformed minimap is dropped and the map still loads.
func LoadFromReader(r io.ReadSeeker) (*Map, error) {
	m := &Map{}

	if err := binary.Read(r, binary.LittleEndian, &m.Header); err != nil {
		return nil, fmt.Errorf("failed to read TNT header: %w", err)
	}

	switch m.Header.IDVersion {
	case VersionTA, VersionLegacy:
		// Full TA parse below.
	case VersionTAK:
		// TA: Kingdoms reuses the TNT container but repurposes the header
		// pointers: no tile mosaic, instead a texture-mapped Graphic-Unit
		// grid plus DataUnit-resolution heightmap and feature grid.  The
		// tak subpackage owns that layout.
		return loadTAK(r, m)
	default:
		return nil, fmt.Errorf("unsupported TNT version: %#x (expected %#x or %#x for TA, %#x for TA:K)",
			m.Header.IDVersion, VersionTA, VersionLegacy, VersionTAK)
	}

	size, err := streamSize(r)
	if err != nil {
		return nil, fmt.Errorf("failed to size TNT stream: %w", err)
	}

	m.TileW = int(m.Header.Width) / 2
	m.TileH = int(m.Header.Height) / 2
	m.AttrW = int(m.Header.Width)
	m.AttrH = int(m.Header.Height)

	legacy := m.IsLegacy()
	recSize := uint64(attrRecordSize)
	if legacy {
		recSize = legacyAttrRecordSize
	}
	tileCells := uint64(m.TileW) * uint64(m.TileH)
	attrCells := uint64(m.Header.Width) * uint64(m.Header.Height)
	attrBytes, ok := mulUint64(attrCells, recSize)
	if !ok {
		return nil, fmt.Errorf("TNT attribute block size overflows: %dx%d cells", m.Header.Width, m.Header.Height)
	}
	tileMap := section{name: "tile map", off: m.Header.PTRMapData, n: tileCells * 2}
	attrs := section{name: "attribute block", off: m.Header.PTRMapAttr, n: attrBytes}
	gfx := section{name: "tile graphics", off: m.Header.PTRTileGfx, n: uint64(m.Header.Tiles) * TileGfxSize}
	feats := section{name: "feature table", off: m.Header.PTRTileAnim, n: uint64(m.Header.TileAnims) * TileAnimEntrySize}
	for _, s := range []section{tileMap, attrs, gfx, feats} {
		if err := s.check(size); err != nil {
			return nil, err
		}
	}

	// Read tile index map (TileW × TileH uint16 entries).
	raw, err := readSection(r, tileMap)
	if err != nil {
		return nil, fmt.Errorf("failed to read tile map: %w", err)
	}
	m.TileMap = make([]uint16, tileCells)
	for i := range m.TileMap {
		m.TileMap[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}

	// Capture the padding between the tile-index block and the attribute
	// block so a shipped file round-trips byte for byte.
	if pad := m.mapDataPadSection(tileMap, attrs, gfx, feats); pad.n > 0 {
		if m.MapDataPad, err = readSection(r, pad); err != nil {
			return nil, fmt.Errorf("failed to read mapdata padding: %w", err)
		}
	}

	// Read tile attributes (AttrW × AttrH entries at 16px resolution).
	raw, err = readSection(r, attrs)
	if err != nil {
		return nil, fmt.Errorf("failed to read attribute block: %w", err)
	}
	m.TileAttr = make([]TileAttr, attrCells)
	if legacy {
		m.LegacyAttr = make([]LegacyTileAttr, attrCells)
		for i := range m.TileAttr {
			copy(m.LegacyAttr[i][:], raw[i*legacyAttrRecordSize:])
			m.TileAttr[i] = m.LegacyAttr[i].tileAttr()
		}
	} else {
		for i := range m.TileAttr {
			rec := raw[i*attrRecordSize:]
			m.TileAttr[i] = TileAttr{
				Height:  rec[0],
				Feature: binary.LittleEndian.Uint16(rec[1:3]),
				Pad:     rec[3],
			}
		}
	}

	// Read tile graphics. Each tile is a capped sub-slice of one buffer.
	raw, err = readSection(r, gfx)
	if err != nil {
		return nil, fmt.Errorf("failed to read tile graphics: %w", err)
	}
	m.Tiles = make([][]byte, m.Header.Tiles)
	for i := range m.Tiles {
		m.Tiles[i] = raw[i*TileGfxSize : (i+1)*TileGfxSize : (i+1)*TileGfxSize]
	}

	// Read the minimap when the header flags one. A flagged minimap that is
	// out of range or truncated is dropped; it is only a preview, so the map
	// itself still loads.
	if m.Header.MinimapFlags()&MinimapPresent != 0 {
		m.readMinimap(r, m.minimapPtr(), size)
	}

	return m, nil
}

// IsLegacy reports whether the map was read from a 0x1020 file.
func (m *Map) IsLegacy() bool { return m.Header.IDVersion == VersionLegacy }

// MinimapFlags returns the header word whose bit 0 (MinimapPresent) says
// whether the file stores a minimap: the word at 0x2c (Unknown1), or 0x3c
// (Pad4) in a 0x1020 file. TA: Kingdoms files keep their minimap pointer in
// the 0x2c slot and have no presence flags, so the result is 0 for them.
func (h *Header) MinimapFlags() uint32 {
	switch h.IDVersion {
	case VersionLegacy:
		return h.Pad4
	case VersionTAK:
		return 0
	}
	return h.Unknown1
}

// minimapPtr returns the header's minimap pointer: the word at 0x28, or 0x38
// for a 0x1020 map.
func (m *Map) minimapPtr() uint32 {
	if m.IsLegacy() {
		return m.Header.Pad3
	}
	return m.Header.PTRMinimap
}

// tileAttr converts a 0x1020 record to the 0x2000 attribute form.
func (a LegacyTileAttr) tileAttr() TileAttr {
	f := uint16(a[2])
	if a[2] >= legacyFeatureSentinelFloor {
		f = FeatureNone
	}
	return TileAttr{Height: a[0], Feature: f}
}

// Attribute record sizes of the 0x2000 and 0x1020 layouts.
const (
	attrRecordSize       = 4
	legacyAttrRecordSize = 8
)

// mulUint64 returns a*b and whether it fits in a uint64.
func mulUint64(a, b uint64) (uint64, bool) {
	hi, lo := bits.Mul64(a, b)
	return lo, hi == 0
}

// section is one byte range of a TNT file named by a header pointer.
type section struct {
	name string
	off  uint32
	n    uint64
}

// check reports an error when a non-empty section runs past size. An empty
// section passes whatever its pointer holds.
func (s section) check(size int64) error {
	if s.n == 0 {
		return nil
	}
	if s.n > uint64(size) || uint64(s.off) > uint64(size)-s.n {
		return fmt.Errorf("TNT %s (%d bytes at offset %d) runs past the end of the file (%d bytes)",
			s.name, s.n, s.off, size)
	}
	return nil
}

// overlaps reports whether the non-empty section shares a byte with
// [start, end).
func (s section) overlaps(start, end uint64) bool {
	if s.n == 0 {
		return false
	}
	return uint64(s.off) < end && uint64(s.off)+s.n > start
}

// mapDataPadSection returns the bytes between the end of the tile map and
// the start of the attribute block when they hold nothing else. A gap that
// another section overlaps is not padding: those bytes are written again
// with their own section, so capturing them would duplicate them.
func (m *Map) mapDataPadSection(tileMap, attrs section, others ...section) section {
	if tileMap.n == 0 || attrs.n == 0 {
		return section{}
	}
	start := uint64(tileMap.off) + tileMap.n
	end := uint64(attrs.off)
	if end <= start {
		return section{}
	}
	for _, o := range others {
		if o.overlaps(start, end) {
			return section{}
		}
	}
	if m.Header.MinimapFlags()&MinimapPresent != 0 && (section{off: m.minimapPtr(), n: 8}).overlaps(start, end) {
		return section{}
	}
	return section{name: "mapdata padding", off: uint32(start), n: end - start}
}

// streamSize returns the length of r and rewinds it to the start.
func streamSize(r io.Seeker) (int64, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return size, nil
}

// readSection reads the whole of s. The caller has checked it against the
// stream length.
func readSection(r io.ReadSeeker, s section) ([]byte, error) {
	buf := make([]byte, s.n)
	if s.n == 0 {
		return buf, nil
	}
	if _, err := r.Seek(int64(s.off), io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// loadTAK parses a TA: Kingdoms TNT. TA:K reuses the TNT container but stores
// Width/Height in 16px DataUnits and renders terrain by texture-mapping a grid
// of 32px Graphic Units: each unit names a JPG texture (0x20) plus a U/V offset
// (0x24/0x28) into it. A DataUnit-resolution heightmap (0x10) and feature grid
// (0x14) accompany a feature name table (0x18 / count 0x1c) and an embedded
// minimap (0x2c). Every section is read by its own header pointer and length.
func loadTAK(r io.ReadSeeker, m *Map) (*Map, error) {
	// The 0x4000 read/write variance lives in the tak subpackage; copy its
	// decoded sections onto the shared Map so existing consumers (rendering,
	// studio backdrop, save) keep working against Map's TAK* fields.
	tm, err := tak.Decode(r)
	if err != nil {
		return nil, err
	}
	m.IsTAK = true
	m.TAKW, m.TAKH = tm.W, tm.H
	m.TAKGUW, m.TAKGUH = tm.GUW, tm.GUH
	m.TAKHeight = tm.Height
	m.TAKFeatureGrid = tm.FeatureGrid
	m.TAKTerrainNames = tm.TerrainNames
	m.TAKUMap = tm.UMap
	m.TAKVMap = tm.VMap
	m.TAKFeatureTableRaw = tm.FeatureTableRaw
	m.Minimap = tm.Minimap
	m.MinimapW, m.MinimapH = tm.MinimapW, tm.MinimapH
	return m, nil
}

// RenderTileMap renders the full map as an RGBA image.
func (m *Map) RenderTileMap(palette color.Palette) *image.RGBA {
	w := m.TileW * 32
	h := m.TileH * 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for ty := 0; ty < m.TileH; ty++ {
		for tx := 0; tx < m.TileW; tx++ {
			tileIdx := m.TileMap[ty*m.TileW+tx]
			if int(tileIdx) >= len(m.Tiles) {
				continue
			}
			tile := m.Tiles[tileIdx]
			ox, oy := tx*32, ty*32
			for py := 0; py < 32; py++ {
				for px := 0; px < 32; px++ {
					palIdx := tile[py*32+px]
					c := color.RGBA{0, 0, 0, 255}
					if int(palIdx) < len(palette) {
						r, g, b, a := palette[palIdx].RGBA()
						c = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
					}
					img.Set(ox+px, oy+py, c)
				}
			}
		}
	}
	return img
}

// RenderHeightMap renders elevation data as a normalized greyscale image.
// The image is AttrW × AttrH (16px resolution, 2× the tile grid). TA:K maps
// render from their DataUnit heightmap at the same 16px resolution.
func (m *Map) RenderHeightMap() *image.Gray {
	if m.IsTAK {
		return m.RenderTAKHeightmap()
	}
	if m.TileAttr == nil {
		return nil
	}
	img := image.NewGray(image.Rect(0, 0, m.AttrW, m.AttrH))

	minH, maxH := uint8(255), uint8(0)
	for _, a := range m.TileAttr {
		if a.Height < minH {
			minH = a.Height
		}
		if a.Height > maxH {
			maxH = a.Height
		}
	}

	for ay := 0; ay < m.AttrH; ay++ {
		for ax := 0; ax < m.AttrW; ax++ {
			h := m.TileAttr[ay*m.AttrW+ax].Height
			v := uint8(0)
			if maxH > minH {
				v = uint8(uint16(h-minH) * 255 / uint16(maxH-minH))
			}
			img.SetGray(ax, ay, color.Gray{v})
		}
	}
	return img
}

// RenderMinimap renders the minimap as an RGBA image.
// Void pixels (palette index 0x64) are rendered as transparent.
func (m *Map) RenderMinimap(palette color.Palette) *image.RGBA {
	if m.Minimap == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, m.MinimapW, m.MinimapH))
	for y := 0; y < m.MinimapH; y++ {
		for x := 0; x < m.MinimapW; x++ {
			palIdx := m.Minimap[y*m.MinimapW+x]
			if palIdx == MinimapVoidByte {
				// Void/padding — transparent.
				continue
			}
			c := color.RGBA{0, 0, 0, 255}
			if int(palIdx) < len(palette) {
				r, g, b, a := palette[palIdx].RGBA()
				c = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// Feature is a named feature type from the TileAnim table.  Raw preserves
// the full 128-byte name buffer including any uninitialised scratch memory
// past the null terminator, so the table can round-trip byte-for-byte.  When
// Raw is empty the writer falls back to writing Name zero-padded.
type Feature struct {
	Index int
	Name  string
	Raw   [128]byte
}

// FeaturePlacement is a placed feature instance on the map.
type FeaturePlacement struct {
	FeatureIdx int // Index into Features
	AttrX      int // Attribute cell X (16px units)
	AttrY      int // Attribute cell Y (16px units)
	PixelX     int // Pixel X (AttrX * 16)
	PixelY     int // Pixel Y (AttrY * 16)
}

// LoadFeatures reads the feature name table. TA stores the table pointer at
// 0x20 (PTRTileAnim); TA:K stores it at 0x18. Both use the same count field
// (0x1c) and the same 4-byte-index + 128-byte-name entry layout. A table that
// runs past the end of r is an error.
func (m *Map) LoadFeatures(r io.ReadSeeker) ([]Feature, error) {
	count := m.Header.TileAnims
	if count == 0 {
		return nil, nil
	}
	tablePtr := m.Header.PTRTileAnim
	if m.IsTAK {
		tablePtr = m.Header.Tiles // TA:K keeps the feature-name table at 0x18
	}
	size, err := streamSize(r)
	if err != nil {
		return nil, err
	}
	table := section{name: "feature table", off: tablePtr, n: uint64(count) * TileAnimEntrySize}
	if err := table.check(size); err != nil {
		return nil, err
	}
	raw, err := readSection(r, table)
	if err != nil {
		return nil, err
	}

	features := make([]Feature, count)
	for i := range features {
		rec := raw[i*TileAnimEntrySize : (i+1)*TileAnimEntrySize]
		var rawName [128]byte
		copy(rawName[:], rec[4:])
		name := string(rawName[:])
		if nul := strings.IndexByte(name, 0); nul >= 0 {
			name = name[:nul]
		}
		features[i] = Feature{Index: int(binary.LittleEndian.Uint32(rec[0:4])), Name: name, Raw: rawName}
	}
	return features, nil
}

// GetFeaturePlacements returns the features the map places, in row-major
// cell order.
//
// For TA maps a cell places a feature when its word is below the feature
// table's count (Header.TileAnims) and below FeatureSentinelFloor, as in the
// game; other words (FeatureNone, FeatureVoid, other sentinels, and indices
// beyond the table) place nothing. A map built in code must therefore set
// Header.TileAnims (Save and Pack do), or use FeaturePlacementsFor.
// TA: Kingdoms maps list every feature-grid value below 0xFF00.
func (m *Map) GetFeaturePlacements() []FeaturePlacement {
	if m.IsTAK {
		// TA:K stores placements in its DataUnit feature grid rather than
		// the TA attribute array; same 16px cell resolution either way.
		return m.TAKFeaturePlacements()
	}
	return m.FeaturePlacementsFor(m.featureTableSize())
}

// FeaturePlacementsFor is GetFeaturePlacements for a feature table of
// featureCount entries. A negative featureCount means the table size is
// unknown, and then every word below the sentinel floor (0xFF00 for TA:
// Kingdoms) counts as a placement.
func (m *Map) FeaturePlacementsFor(featureCount int) []FeaturePlacement {
	w, h, cells := m.featureGrid()
	if cells == nil {
		return nil
	}
	floor := FeatureSentinelFloor
	if m.IsTAK {
		floor = takNoFeature
	}
	scale := 16
	if m.IsTAK {
		scale = TAKDataUnit
	}
	var placements []FeaturePlacement
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			f := cells(y*w + x)
			if f >= floor || (featureCount >= 0 && int(f) >= featureCount) {
				continue
			}
			placements = append(placements, FeaturePlacement{
				FeatureIdx: int(f),
				AttrX:      x,
				AttrY:      y,
				PixelX:     x * scale,
				PixelY:     y * scale,
			})
		}
	}
	return placements
}

// featureGrid returns the feature grid's size and an accessor for its
// words, or a nil accessor when the map has none.
func (m *Map) featureGrid() (w, h int, word func(int) uint16) {
	if m.IsTAK {
		if m.TAKFeatureGrid == nil || m.TAKW == 0 || len(m.TAKFeatureGrid) < m.TAKW*m.TAKH {
			return 0, 0, nil
		}
		return m.TAKW, m.TAKH, func(i int) uint16 { return m.TAKFeatureGrid[i] }
	}
	if m.TileAttr == nil || m.AttrW == 0 || len(m.TileAttr) < m.AttrW*m.AttrH {
		return 0, 0, nil
	}
	return m.AttrW, m.AttrH, func(i int) uint16 { return m.TileAttr[i].Feature }
}

// featureTableSize returns the feature count the header records.
func (m *Map) featureTableSize() int {
	return int(m.Header.TileAnims)
}

// PlacesFeature reports whether a TA attribute word places a feature from a
// table of featureCount entries: it must be below featureCount and below
// FeatureSentinelFloor.
func PlacesFeature(word uint16, featureCount int) bool {
	return word < FeatureSentinelFloor && int(word) < featureCount
}

// takGraphicUnit is the pixel size of a TA: Kingdoms Graphic Unit (terrain
// texture tile). TAKDataUnit is half this: the DataUnit grid (heightmap and
// feature placement) is twice as fine as the Graphic-Unit terrain grid.
const takGraphicUnit = 32

// TAKDataUnit is the pixel size of a TA: Kingdoms DataUnit — the unit of the
// heightmap and feature-placement grids. Feature placements scale by this to
// reach full-resolution terrain pixels.
const TAKDataUnit = 16

// TAKPixelW and TAKPixelH report the full-resolution terrain render dimensions
// in pixels (Graphic-Unit grid × 32px).
func (m *Map) TAKPixelW() int { return m.TAKGUW * takGraphicUnit }
func (m *Map) TAKPixelH() int { return m.TAKGUH * takGraphicUnit }

// TAKFeaturePlacements returns every placed feature in a TA: Kingdoms map, read
// from the DataUnit-resolution feature grid. PixelX/PixelY scale the DataUnit
// cell (16px) up to the full-resolution terrain render, where feature sprites
// are anchored.
func (m *Map) TAKFeaturePlacements() []FeaturePlacement {
	if m.TAKFeatureGrid == nil || m.TAKW == 0 {
		return nil
	}
	var placements []FeaturePlacement
	for y := 0; y < m.TAKH; y++ {
		for x := 0; x < m.TAKW; x++ {
			v := m.TAKFeatureGrid[y*m.TAKW+x]
			if v >= takNoFeature {
				continue
			}
			placements = append(placements, FeaturePlacement{
				FeatureIdx: int(v),
				AttrX:      x,
				AttrY:      y,
				PixelX:     x * TAKDataUnit,
				PixelY:     y * TAKDataUnit,
			})
		}
	}
	return placements
}

// RenderTAKTerrain composites the full-resolution TA: Kingdoms terrain by
// copying a 32×32 tile from each Graphic Unit's source texture at its U/V
// offset. tex resolves a terrain name to its decoded texture image (the caller
// supplies the JPGs, typically from a VFS); a nil return leaves that unit's
// tile blank so missing textures are visible rather than fatal. Returns nil
// when the map carries no terrain-name table.
func (m *Map) RenderTAKTerrain(tex func(name uint32) image.Image) *image.RGBA {
	if !m.IsTAK || m.TAKGUW == 0 || m.TAKTerrainNames == nil {
		return nil
	}
	const gu = takGraphicUnit
	img := image.NewRGBA(image.Rect(0, 0, m.TAKPixelW(), m.TAKPixelH()))
	cache := make(map[uint32]image.Image)
	for gy := 0; gy < m.TAKGUH; gy++ {
		for gx := 0; gx < m.TAKGUW; gx++ {
			i := gy*m.TAKGUW + gx
			name := m.TAKTerrainNames[i]
			t, ok := cache[name]
			if !ok {
				t = tex(name)
				cache[name] = t
			}
			if t == nil {
				continue
			}
			sx := int(m.TAKUMap[i]) * gu
			sy := int(m.TAKVMap[i]) * gu
			dst := image.Rect(gx*gu, gy*gu, gx*gu+gu, gy*gu+gu)
			draw.Draw(img, dst, t, image.Point{X: sx, Y: sy}, draw.Src)
		}
	}
	return img
}

// RenderTAKHeightmap renders the TA: Kingdoms heightmap as a normalised
// greyscale image at DataUnit resolution (TAKW×TAKH). It needs no external
// assets, so it is the self-contained fallback when terrain textures are
// unavailable.
func (m *Map) RenderTAKHeightmap() *image.Gray {
	if m.TAKHeight == nil || m.TAKW == 0 {
		return nil
	}
	minH, maxH := uint8(255), uint8(0)
	for _, h := range m.TAKHeight {
		if h < minH {
			minH = h
		}
		if h > maxH {
			maxH = h
		}
	}
	span := int(maxH) - int(minH)
	img := image.NewGray(image.Rect(0, 0, m.TAKW, m.TAKH))
	for i, h := range m.TAKHeight {
		v := uint8(0)
		if span > 0 {
			v = uint8((int(h) - int(minH)) * 255 / span)
		}
		img.Pix[i] = v
	}
	return img
}

// WritePNG encodes an image to PNG format.
func WritePNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}
