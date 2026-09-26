// Package tnt reads and writes Total Annihilation TNT map files.
//
// # Versions
//
// The first header word is the version. LoadFromReader accepts three:
//
//   - 0x2000 (VersionTA): the layout TA 3.1c writes and every shipped map
//     uses. Save writes this version.
//   - 0x1020 (VersionLegacy): an older layout TA 3.1c also loads. Its
//     attribute records are 8 bytes (height at +0, an 8-bit feature at +2)
//     and its minimap pointer and presence flags are the words at 0x38 and
//     0x3c. It is read into the 0x2000 form (see Map.LegacyAttr) and Save
//     converts it to 0x2000.
//   - 0x4000 (VersionTAK): TA: Kingdoms, which reuses the container with a
//     different layout (see the tak subpackage and Map.IsTAK). TA 3.1c does
//     not load these maps; SaveTAK writes them.
//
// Any other version is an error.
//
// # Layout (0x2000)
//
// Width and Height count 16-pixel attribute cells. The tile map holds
// (Width/2)×(Height/2) little-endian uint16 indices into the tile set of
// Tiles 32×32 palette-index graphics (1024 bytes each). There is one 4-byte
// attribute per cell: height, a uint16 feature word, and a pad byte. The
// feature table holds TileAnims 132-byte records, a uint32 followed by a
// 128-byte NUL-terminated feature name; the game uses only the name. Every
// section is read from its header pointer; a section with no bytes is never
// read, whatever its pointer holds.
//
// # Feature words
//
// A cell's feature word places the feature-table entry it indexes only when
// it is below the table's count (Header.TileAnims) and below
// FeatureSentinelFloor (0xFFFB); every other word places nothing. Of those,
// FeatureVoid (0xFFFC) marks a void cell that cannot be crossed or built on,
// and FeatureNone (0xFFFF) is an empty cell; shipped maps also contain
// 0xFFFE, which the game ignores. GetFeaturePlacements, FeatureCounts and
// RenderBuildMap follow this rule, Save refuses words below the floor that
// name no table entry, and Lint reports them.
//
// # Minimap
//
// The header word at 0x2c holds presence flags: the game reads the minimap
// at the 0x28 pointer (an 8-byte width and height, then palette indices)
// only when bit 0 (MinimapPresent) is set, and otherwise builds its radar
// picture from the tiles. Shipped minimaps are 252 pixels wide (MinimapSize)
// and 252 or 256 tall, with the map drawn into the top-left corner (see
// MinimapContentSize) and padding, normally palette index 0x64, elsewhere.
// The game's radar uses a stored minimap only when both sides are at least
// 252. LoadFromReader drops a flagged minimap whose sides are 0 or over
// 1024 or whose pixels run past the end of the file, and keeps the map.
// Save sets or clears bit 0 to match the minimap it writes, and
// BuildMinimap renders one in the shipped layout.
//
// # Tile indices
//
// The game loads a map whose tile map names a tile beyond its tile set and
// reads past the end of the set for it. LoadFromReader accepts such maps
// too; RenderTileMap leaves those tiles blank, Optimize leaves the indices
// alone, and Lint reports them as bad-tile-index. Save and Pack refuse them
// unless SaveOptions / PackOptions AllowUnresolvedIndices is set.
//
// # Malformed files
//
// LoadFromReader checks every section against the length of the file
// before allocating it: a tile map, attribute block, tile set or feature
// table that runs past the end of the file is an error, never zero-filled
// data. Save refuses a tile grid that is not half the attribute grid, more
// than MaxTiles tiles, a feature table of FeatureSentinelFloor or more
// entries, feature names of 128 bytes or more, and minimap sides over 1024.
//
// # Interchange bounds
//
// Some readers of the format refuse files over 256 MiB
// (InterchangeMaxFileSize), maps over 4096 attribute cells on a side
// (InterchangeMaxAttrSide), more than 65,536 tiles or features, or minimaps
// over 1024 on a side. Shipped maps are far smaller. kbot-io reads larger
// files; Save refuses the tile, feature and minimap cases above and reports
// the others through SaveOptions.Warn, and Lint reports them as
// interchange-bounds.
package tnt
