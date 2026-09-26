// Package sct implements reading of Total Annihilation SCT (Section) map files.
//
// SCT files contain tile-based terrain sections used by the TA map editor.
// Each section has a grid of 32×32 pixel tiles, height data, and a 128×128 minimap.
package sct

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
)

// Header is the 28-byte SCT file header.
type Header struct {
	Version    uint32 // Always 3
	PtrMinimap uint32 // Offset to 128×128 minimap
	NumTiles   uint32 // Number of 32×32 tiles
	PtrTiles   uint32 // Offset to tile pixel data
	Width      uint32 // Section width in tiles
	Height     uint32 // Section height in tiles
	PtrData    uint32 // Offset to section data (tile indices + height)
}

// HeightData is one height sample (4 per tile).
// In V3 files this is 4 bytes; in V2 files it is 8 bytes.
type HeightData struct {
	Height uint8
}

// Section is a parsed SCT file.
type Section struct {
	Header    Header
	Tiles     [][]byte     // NumTiles entries, each 1024 bytes (32×32 palette indices)
	TileMap   []int16      // Width×Height tile indices into Tiles
	HeightMap []HeightData // (Width*2)×(Height*2) height samples at 16px resolution
	AttrW     int          // Width * 2 (attribute grid width)
	AttrH     int          // Height * 2 (attribute grid height)
	Minimap   []byte       // 128×128 palette indices
}

// tileBytes is the size of one 32×32 tile graphic.
const tileBytes = 32 * 32

// minimapBytes is the size of the 128×128 minimap.
const minimapBytes = 128 * 128

// LoadFromReader parses an SCT file from the given reader. Header pointers are
// absolute stream offsets.
//
// The stream is sized first and every table the header describes (tile
// graphics, tile map and height table) is checked against it before anything
// is allocated, so a corrupt header fails with an error instead of a huge
// allocation. A tile map or height table that runs past the end of the file is
// an error; a missing or truncated minimap leaves Minimap nil.
func LoadFromReader(r io.ReadSeeker) (*Section, error) {
	s := &Section{}

	// Read header.
	if err := binary.Read(r, binary.LittleEndian, &s.Header); err != nil {
		return nil, fmt.Errorf("failed to read SCT header: %w", err)
	}
	if s.Header.Version != 2 && s.Header.Version != 3 {
		return nil, fmt.Errorf("unsupported SCT version: %d (expected 2 or 3)", s.Header.Version)
	}
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to size SCT stream: %w", err)
	}
	size := uint64(end)
	h := s.Header

	// Size every table in 64 bits and check it against the file.
	cells := uint64(h.Width) * uint64(h.Height)
	if cells > size/2 {
		return nil, fmt.Errorf("section of %dx%d tiles needs a larger tile map than the %d-byte file holds",
			h.Width, h.Height, size)
	}
	entrySize := uint64(4) // V3: 4 bytes per height entry, V2: 8
	if h.Version == 2 {
		entrySize = 8
	}
	graphics := uint64(h.NumTiles) * tileBytes
	tileMap := cells * 2
	heights := cells * 4 * entrySize
	for _, sec := range []struct {
		name   string
		off, n uint64
	}{
		{"tile graphics", uint64(h.PtrTiles), graphics},
		{"tile map", uint64(h.PtrData), tileMap},
		{"height table", uint64(h.PtrData) + tileMap, heights},
	} {
		if sec.off > size || sec.n > size-sec.off {
			return nil, fmt.Errorf("%s (%d bytes at 0x%X) runs past the end of the %d-byte file",
				sec.name, sec.n, sec.off, size)
		}
	}

	// Read tiles.
	if _, err := r.Seek(int64(h.PtrTiles), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to tiles: %w", err)
	}
	pixels := make([]byte, graphics)
	if _, err := io.ReadFull(r, pixels); err != nil {
		return nil, fmt.Errorf("failed to read tiles: %w", err)
	}
	s.Tiles = make([][]byte, h.NumTiles)
	for i := range s.Tiles {
		s.Tiles[i] = pixels[i*tileBytes : (i+1)*tileBytes : (i+1)*tileBytes]
	}

	// Read section data (tile indices).
	if _, err := r.Seek(int64(h.PtrData), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to section data: %w", err)
	}
	s.TileMap = make([]int16, cells)
	if err := binary.Read(r, binary.LittleEndian, s.TileMap); err != nil {
		return nil, fmt.Errorf("failed to read tile map: %w", err)
	}

	// Read height/attribute data.
	// The attribute grid is Width*2 × Height*2 (16px resolution).
	// Height data follows immediately after the tile map.
	s.AttrW = int(h.Width) * 2
	s.AttrH = int(h.Height) * 2
	raw := make([]byte, heights)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, fmt.Errorf("failed to read height table: %w", err)
	}
	s.HeightMap = make([]HeightData, cells*4)
	for i := range s.HeightMap {
		s.HeightMap[i] = HeightData{Height: raw[uint64(i)*entrySize]}
	}

	// Read minimap.
	if h.PtrMinimap > 0 && uint64(h.PtrMinimap)+minimapBytes <= size {
		if _, err := r.Seek(int64(h.PtrMinimap), io.SeekStart); err == nil {
			minimap := make([]byte, minimapBytes)
			if _, err := io.ReadFull(r, minimap); err == nil {
				s.Minimap = minimap
			}
		}
	}

	return s, nil
}

// RenderTileMap renders the full tile map as an RGBA image using the given palette.
func (s *Section) RenderTileMap(palette color.Palette) *image.RGBA {
	w := int(s.Header.Width) * 32
	h := int(s.Header.Height) * 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for ty := 0; ty < int(s.Header.Height); ty++ {
		for tx := 0; tx < int(s.Header.Width); tx++ {
			cell := ty*int(s.Header.Width) + tx
			if cell >= len(s.TileMap) {
				continue
			}
			tileIdx := s.TileMap[cell]
			if tileIdx < 0 || int(tileIdx) >= len(s.Tiles) || len(s.Tiles[tileIdx]) < tileBytes {
				continue
			}
			tile := s.Tiles[tileIdx]
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

// RenderHeightMap renders the height data as a normalized greyscale image.
// The attribute grid is AttrW × AttrH (2× the tile grid, at 16px resolution).
func (s *Section) RenderHeightMap() *image.Gray {
	if s.HeightMap == nil {
		return nil
	}
	img := image.NewGray(image.Rect(0, 0, s.AttrW, s.AttrH))

	minH, maxH := uint8(255), uint8(0)
	for _, hd := range s.HeightMap {
		if hd.Height < minH {
			minH = hd.Height
		}
		if hd.Height > maxH {
			maxH = hd.Height
		}
	}

	for ay := 0; ay < s.AttrH; ay++ {
		for ax := 0; ax < s.AttrW; ax++ {
			idx := ay*s.AttrW + ax
			if idx >= len(s.HeightMap) {
				continue
			}
			h := s.HeightMap[idx].Height
			v := uint8(128)
			if maxH > minH {
				v = uint8(uint16(h-minH) * 255 / uint16(maxH-minH))
			}
			img.SetGray(ax, ay, color.Gray{v})
		}
	}
	return img
}

// RenderMinimap renders the minimap as an RGBA image using the given palette.
func (s *Section) RenderMinimap(palette color.Palette) *image.RGBA {
	if len(s.Minimap) < minimapBytes {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			palIdx := s.Minimap[y*128+x]
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

// WritePNG encodes an image to PNG format.
func WritePNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}
