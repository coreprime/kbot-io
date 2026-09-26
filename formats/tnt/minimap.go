package tnt

import (
	"encoding/binary"
	"fmt"
	"image/color"
	"io"
	"math"
)

// MinimapPresent is bit 0 of the header's minimap presence flags
// (Header.MinimapFlags). The game reads the stored minimap only when it is
// set; otherwise it builds the map's radar picture from the tiles. Save sets
// it whenever it writes a minimap and clears it otherwise.
const MinimapPresent uint32 = 1

// MinimapVoidByte is the palette index used for minimap padding (outside map area).
const MinimapVoidByte = 0x64

// MinimapSize is the edge of the minimap the game's own maps store (some are
// MinimapSize wide and 256 tall). The map is drawn into its top-left corner
// with the longer side MinimapSize long (see MinimapContentSize), and the rest
// is padding, normally MinimapVoidByte. The game's radar reads that top-left
// region of a stored minimap only when both of its sides are at least
// MinimapSize; for a smaller one it builds the radar picture from the tiles.
const MinimapSize = 252

// maxMinimapSide is the largest minimap edge the reader accepts; a larger
// stored minimap is dropped, and Save refuses to write one.
const maxMinimapSide = 1024

// Map edges the game never shows: the visible map is 32 pixels narrower and
// 128 pixels shorter than its tiles.
const (
	hiddenRightEdge  = 32
	hiddenBottomEdge = 128
)

// MinimapContentSize returns the size, in minimap pixels, of the top-left
// minimap region that shows a TA map of attrW×attrH 16-pixel attribute cells
// (Header.Width × Header.Height), for a MinimapSize minimap. It returns 0, 0
// for an empty map.
//
// The region covers the visible map: the attrW*16 by attrH*16 pixel map less
// the 32-pixel right and 128-pixel bottom edges the game never shows. Its
// longer side is MinimapSize and the shorter side keeps the visible map's
// aspect ratio, rounded down, so a 450×392-cell map (225×196 tiles) gets a
// 252×216 region. A map too small to have a visible area uses its full pixel
// size instead.
func MinimapContentSize(attrW, attrH int) (w, h int) {
	return minimapRegion(attrW, attrH, MinimapSize, MinimapSize)
}

// minimapRegion is MinimapContentSize for a maxW×maxH minimap: the longer
// side is MinimapSize or the minimap's own side along it, whichever is
// smaller, and the result never exceeds the minimap.
func minimapRegion(attrW, attrH, maxW, maxH int) (w, h int) {
	vw, vh := visibleMapPixels(attrW, attrH)
	if vw <= 0 || vh <= 0 || maxW <= 0 || maxH <= 0 {
		return 0, 0
	}
	if vw < vh {
		h = min(MinimapSize, maxH)
		w = int(int64(h) * vw / vh)
	} else {
		w = min(MinimapSize, maxW)
		h = int(int64(w) * vh / vw)
	}
	return max(min(w, maxW), 1), max(min(h, maxH), 1)
}

// visibleMapPixels returns the pixel size of the part of the map the game
// shows, or the full pixel size when the map is too small for its hidden
// edges.
func visibleMapPixels(attrW, attrH int) (vw, vh int64) {
	fw, fh := int64(attrW)*16, int64(attrH)*16
	vw, vh = fw-hiddenRightEdge, fh-hiddenBottomEdge
	if vw <= 0 || vh <= 0 {
		return fw, fh
	}
	return vw, vh
}

// MinimapContentBounds returns the size of the region at the top left of the
// minimap that shows the map; the rest is padding.
//
// For TA maps the region comes from the map's dimensions, as the game derives
// it (see MinimapContentSize), capped to the stored minimap; a minimap
// smaller than MinimapSize is assumed to use its own size as the longer
// side. It does not depend on the pixels: some shipped minimaps draw the map
// a few pixels larger or smaller, or pad with another colour, and the game
// still reads this region. TA: Kingdoms minimaps are measured by scanning the
// first row and column for the MinimapVoidByte padding.
func (m *Map) MinimapContentBounds() (contentW, contentH int) {
	if m.Minimap == nil || m.MinimapW <= 0 || m.MinimapH <= 0 {
		return 0, 0
	}
	if m.IsTAK {
		return m.scanMinimapContent()
	}
	return minimapRegion(m.AttrW, m.AttrH, m.MinimapW, m.MinimapH)
}

// scanMinimapContent measures the minimap content by scanning the first row
// from the right and the first column from the bottom for the last pixel
// that is not MinimapVoidByte.
func (m *Map) scanMinimapContent() (contentW, contentH int) {
	for x := m.MinimapW - 1; x >= 0; x-- {
		if m.Minimap[x] != MinimapVoidByte {
			contentW = x + 1
			break
		}
	}
	for y := m.MinimapH - 1; y >= 0; y-- {
		if m.Minimap[y*m.MinimapW] != MinimapVoidByte {
			contentH = y + 1
			break
		}
	}
	return contentW, contentH
}

// BuildMinimap renders the map's minimap from its tiles and stores it in
// m.Minimap, laid out as the game's own maps are: a MinimapSize×MinimapSize
// image whose top-left MinimapContentSize region shows the visible map and
// whose remainder is MinimapVoidByte.
//
// palette gives the colours of the tile palette indices (normally the game
// palette). Each minimap pixel is the average colour of the map pixels it
// covers, matched back to the nearest palette entry other than
// MinimapVoidByte, so that no map pixel reads as padding. Very large maps
// sample at most 32 map pixels along each axis of a minimap pixel. Map pixels
// under a tile index outside the tile set are skipped. It returns an error
// for a TA: Kingdoms map, an empty palette or an inconsistent tile grid.
func (m *Map) BuildMinimap(palette color.Palette) error {
	if m.IsTAK {
		return fmt.Errorf("BuildMinimap: TA:K maps are not tile based")
	}
	if len(palette) == 0 {
		return fmt.Errorf("BuildMinimap: empty palette")
	}
	if m.TileW <= 0 || m.TileH <= 0 || len(m.TileMap) != m.TileW*m.TileH {
		return fmt.Errorf("BuildMinimap: invalid tile grid %dx%d with %d entries", m.TileW, m.TileH, len(m.TileMap))
	}
	cw, ch := MinimapContentSize(m.AttrW, m.AttrH)
	if cw == 0 || ch == 0 {
		return fmt.Errorf("BuildMinimap: map %dx%d has no visible area", m.AttrW, m.AttrH)
	}
	vw, vh := visibleMapPixels(m.AttrW, m.AttrH)
	vw, vh = min(vw, int64(m.TileW)*32), min(vh, int64(m.TileH)*32)

	rgb := make([][3]int, len(palette))
	for i, c := range palette {
		r, g, b, _ := c.RGBA()
		rgb[i] = [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	}
	nearest := newNearestColour(rgb, MinimapVoidByte)

	pix := make([]byte, MinimapSize*MinimapSize)
	for i := range pix {
		pix[i] = MinimapVoidByte
	}
	for y := 0; y < ch; y++ {
		y0, y1 := spanOf(y, ch, vh)
		sy := max((y1-y0+31)/32, 1)
		for x := 0; x < cw; x++ {
			x0, x1 := spanOf(x, cw, vw)
			sx := max((x1-x0+31)/32, 1)
			var sum [3]int
			n := 0
			for py := y0; py < y1; py += sy {
				row := int(py/32) * m.TileW
				for px := x0; px < x1; px += sx {
					ti := int(m.TileMap[row+int(px/32)])
					if ti >= len(m.Tiles) || len(m.Tiles[ti]) < TileGfxSize {
						continue
					}
					idx := int(m.Tiles[ti][(py%32)*32+px%32])
					if idx >= len(rgb) {
						continue
					}
					c := rgb[idx]
					sum[0] += c[0]
					sum[1] += c[1]
					sum[2] += c[2]
					n++
				}
			}
			if n == 0 {
				pix[y*MinimapSize+x] = 0
				continue
			}
			pix[y*MinimapSize+x] = nearest([3]int{sum[0] / n, sum[1] / n, sum[2] / n})
		}
	}
	m.Minimap = pix
	m.MinimapW, m.MinimapH = MinimapSize, MinimapSize
	return nil
}

// spanOf returns the half-open range of the total source pixels that output
// pixel i of n covers; the range is never empty.
func spanOf(i, n int, total int64) (lo, hi int64) {
	lo = int64(i) * total / int64(n)
	hi = int64(i+1) * total / int64(n)
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// newNearestColour returns a function that maps a colour to the index of the
// closest palette entry (by squared RGB distance), never returning skip.
func newNearestColour(rgb [][3]int, skip int) func([3]int) byte {
	cache := make(map[[3]int]byte)
	return func(c [3]int) byte {
		if v, ok := cache[c]; ok {
			return v
		}
		best, bestD := 0, math.MaxInt
		for i, p := range rgb {
			if i == skip || i > 255 {
				continue
			}
			dr, dg, db := c[0]-p[0], c[1]-p[1], c[2]-p[2]
			if d := dr*dr + dg*dg + db*db; d < bestD {
				best, bestD = i, d
			}
		}
		cache[c] = byte(best)
		return byte(best)
	}
}

// readMinimap reads the minimap stored at ptr, leaving m's minimap empty when
// its header or pixels lie outside the file or a side is 0 or over 1024.
func (m *Map) readMinimap(r io.ReadSeeker, ptr uint32, size int64) {
	hdr := section{off: ptr, n: 8}
	if ptr > math.MaxUint32-8 || hdr.check(size) != nil {
		return
	}
	raw, err := readSection(r, hdr)
	if err != nil {
		return
	}
	mmW := binary.LittleEndian.Uint32(raw[0:4])
	mmH := binary.LittleEndian.Uint32(raw[4:8])
	if mmW == 0 || mmH == 0 || mmW > maxMinimapSide || mmH > maxMinimapSide {
		return
	}
	pix := section{off: ptr + 8, n: uint64(mmW) * uint64(mmH)}
	if pix.check(size) != nil {
		return
	}
	pixels, err := readSection(r, pix)
	if err != nil {
		return
	}
	m.Minimap = pixels
	m.MinimapW = int(mmW)
	m.MinimapH = int(mmH)
}
