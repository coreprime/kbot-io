package sct

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"strings"
	"testing"
)

// Header word offsets used to patch synthetic files.
const (
	offNumTiles = 8
	offWidth    = 16
	offHeight   = 20
)

// synthSCT builds a version 3 section of w×h tiles using two tile graphics,
// with heights counting up from 0 and a minimap.
func synthSCT(w, h int) []byte {
	const headerLen = 28
	tiles := 2
	ptrTiles := headerLen
	ptrData := ptrTiles + tiles*tileBytes
	ptrMinimap := ptrData + w*h*2 + 4*w*h*4
	out := make([]byte, ptrMinimap+minimapBytes)
	for i, v := range []uint32{3, uint32(ptrMinimap), uint32(tiles), uint32(ptrTiles), uint32(w), uint32(h), uint32(ptrData)} {
		binary.LittleEndian.PutUint32(out[i*4:], v)
	}
	for i := 0; i < tileBytes; i++ {
		out[ptrTiles+i] = 1
		out[ptrTiles+tileBytes+i] = 2
	}
	for c := 0; c < w*h; c++ {
		binary.LittleEndian.PutUint16(out[ptrData+2*c:], uint16(c%2))
	}
	for i := 0; i < 4*w*h; i++ {
		out[ptrData+w*h*2+4*i] = byte(i)
	}
	for i := 0; i < minimapBytes; i++ {
		out[ptrMinimap+i] = 7
	}
	return out
}

func TestLoadSynthetic(t *testing.T) {
	s, err := LoadFromReader(bytes.NewReader(synthSCT(3, 2)))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	if len(s.Tiles) != 2 || len(s.TileMap) != 6 || len(s.HeightMap) != 24 || s.AttrW != 6 || s.AttrH != 4 {
		t.Fatalf("tiles %d, map %d, heights %d, attr %dx%d", len(s.Tiles), len(s.TileMap), len(s.HeightMap), s.AttrW, s.AttrH)
	}
	if s.Tiles[1][0] != 2 || s.TileMap[1] != 1 || s.HeightMap[23].Height != 23 || len(s.Minimap) != minimapBytes {
		t.Fatal("decoded values differ from the synthetic file")
	}
	// Appending to one tile must not overwrite the next.
	_ = append(s.Tiles[0], 9)
	if s.Tiles[1][0] != 2 {
		t.Fatal("tiles share writable capacity")
	}
	pal := color.Palette{color.Black, color.White, color.Gray{128}}
	if img := s.RenderTileMap(pal); img.Bounds().Dx() != 96 || img.Bounds().Dy() != 64 {
		t.Fatalf("tile map image %v", img.Bounds())
	}
}

func TestLoadRejectsOversizedTables(t *testing.T) {
	patched := func(patch func(b []byte)) []byte {
		b := synthSCT(3, 2)
		patch(b)
		return b
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"huge dimensions", patched(func(b []byte) {
			binary.LittleEndian.PutUint32(b[offWidth:], 0x10000)
			binary.LittleEndian.PutUint32(b[offHeight:], 0x10000)
		}), "tile map"},
		{"huge tile count", patched(func(b []byte) {
			binary.LittleEndian.PutUint32(b[offNumTiles:], 0xFFFFFFFF)
		}), "tile graphics"},
		// The file ends 50 bytes into the 96-byte height table.
		{"truncated height table", synthSCT(3, 2)[:28+2*tileBytes+12+50], "height table"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadFromReader(bytes.NewReader(c.data))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming the %s", err, c.want)
			}
		})
	}
}

func TestRenderToleratesShortTables(t *testing.T) {
	s := &Section{Header: Header{Width: 4, Height: 4}, TileMap: []int16{0}, Tiles: [][]byte{make([]byte, 10)}}
	if img := s.RenderTileMap(color.Palette{color.Black}); img == nil {
		t.Fatal("nil image")
	}
	if s.RenderMinimap(color.Palette{color.Black}) != nil {
		t.Fatal("minimap rendered without data")
	}
}
