package pal

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
)

// EntryCount is the number of color entries in a TA palette (always 256).
const EntryCount = 256

// FileSize is the size in bytes of a TA palette (256 × 4). The game reads
// exactly this many bytes from the start of a .PAL file.
const FileSize = EntryCount * 4

// ErrEmpty is returned for an empty palette file. The game then takes the
// palette from the PCX of the same name instead; see LoadNamed and FromPCX.
var ErrEmpty = errors.New("pal: empty palette file")

// ErrShort is returned for a palette file of 1 to 1,023 bytes. The game reads
// 1,024 bytes regardless, past the end of such a file, so it has no usable
// palette.
var ErrShort = errors.New("pal: palette file shorter than 1024 bytes")

// Palette is a parsed TA .PAL file. Raw keeps the 1,024 bytes the colours
// came from so the unused fourth byte of each entry survives a round trip.
type Palette struct {
	Colors [EntryCount]color.RGBA
	Raw    []byte
}

// fromEntries builds a Palette from the first FileSize bytes of data.
func fromEntries(data []byte) *Palette {
	p := &Palette{Raw: append([]byte(nil), data[:FileSize]...)}
	for i := 0; i < EntryCount; i++ {
		off := i * 4
		p.Colors[i] = color.RGBA{R: data[off], G: data[off+1], B: data[off+2], A: 255}
	}
	p.Colors[0].A = 0
	return p
}

// LoadFromReader parses a .PAL file from r, reading only the first 1,024
// bytes as the game does. An empty stream fails with ErrEmpty and a shorter
// one with ErrShort.
//
// Color index 0 is reported with alpha=0 to match how every other kbot loader
// treats the TA palette (sprites use index 0 as the transparent key); use
// OpaqueColorModel for terrain and backdrops, which the game draws opaque.
// All other entries are returned fully opaque.
func LoadFromReader(r io.Reader) (*Palette, error) {
	raw := make([]byte, FileSize)
	if n, err := io.ReadFull(r, raw); err != nil {
		switch {
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("read palette: %w", ErrEmpty)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return nil, fmt.Errorf("read palette: %w (got %d)", ErrShort, n)
		}
		return nil, fmt.Errorf("read palette: %w", err)
	}
	return fromEntries(raw), nil
}

// LoadFromBytes parses a .PAL file from a byte slice. As in the game, the
// first 1,024 bytes are used and any further bytes are ignored. Empty data
// fails with ErrEmpty and 1 to 1,023 bytes with ErrShort.
func LoadFromBytes(data []byte) (*Palette, error) {
	switch {
	case len(data) == 0:
		return nil, fmt.Errorf("invalid palette: %w", ErrEmpty)
	case len(data) < FileSize:
		return nil, fmt.Errorf("invalid palette size: %w (got %d)", ErrShort, len(data))
	}
	return fromEntries(data), nil
}

// LoadFromFile parses a .PAL file at path.
func LoadFromFile(path string) (*Palette, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return LoadFromReader(f)
}

// Write encodes p as a 1,024-byte .PAL file. The colours come from Colors;
// the unused fourth byte of each entry is taken from Raw when it holds a full
// palette (so a loaded file round-trips byte for byte) and is zero otherwise,
// matching Cavedog's files.
func (p *Palette) Write(w io.Writer) error {
	buf := make([]byte, FileSize)
	keep := len(p.Raw) >= FileSize
	for i := 0; i < EntryCount; i++ {
		off := i * 4
		buf[off] = p.Colors[i].R
		buf[off+1] = p.Colors[i].G
		buf[off+2] = p.Colors[i].B
		if keep {
			buf[off+3] = p.Raw[off+3]
		}
	}
	_, err := w.Write(buf)
	return err
}

// ColorModel returns the palette as a Go image/color Palette, with index 0
// transparent (the sprite convention).
func (p *Palette) ColorModel() color.Palette {
	out := make(color.Palette, EntryCount)
	for i := 0; i < EntryCount; i++ {
		out[i] = p.Colors[i]
	}
	return out
}

// OpaqueColorModel returns the palette as a Go image/color Palette with every
// entry opaque, index 0 included. The game draws terrain, minimaps and
// backdrops this way; only sprites treat index 0 as transparent.
func (p *Palette) OpaqueColorModel() color.Palette {
	out := make(color.Palette, EntryCount)
	for i := 0; i < EntryCount; i++ {
		c := p.Colors[i]
		c.A = 255
		out[i] = c
	}
	return out
}

// RenderSwatch renders the 256 entries as a 16×16 grid of cellSize×cellSize
// squares.  cellSize<=0 defaults to 16 (256×256 image).
func (p *Palette) RenderSwatch(cellSize int) *image.RGBA {
	if cellSize <= 0 {
		cellSize = 16
	}
	const cols = 16
	img := image.NewRGBA(image.Rect(0, 0, cols*cellSize, cols*cellSize))
	for i := 0; i < EntryCount; i++ {
		col := i % cols
		row := i / cols
		c := color.RGBA{p.Colors[i].R, p.Colors[i].G, p.Colors[i].B, 255}
		if i == 0 {
			// Render index 0 with a magenta hatch so callers can see where the
			// transparent sentinel lives.  Keep one solid corner so the actual
			// stored RGB is still visible.
			for y := 0; y < cellSize; y++ {
				for x := 0; x < cellSize; x++ {
					px := col*cellSize + x
					py := row*cellSize + y
					if (x+y)%2 == 0 {
						img.SetRGBA(px, py, color.RGBA{255, 0, 255, 255})
					} else {
						img.SetRGBA(px, py, c)
					}
				}
			}
			continue
		}
		for y := 0; y < cellSize; y++ {
			for x := 0; x < cellSize; x++ {
				img.SetRGBA(col*cellSize+x, row*cellSize+y, c)
			}
		}
	}
	return img
}

// WritePNG encodes an image to PNG.
func WritePNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}

// WriteJASC encodes the palette in the JASC-PAL plain-text format, the de
// facto exchange format used by Paint Shop Pro, GIMP and most palette editors.
// Color count is always 256.
func (p *Palette) WriteJASC(w io.Writer) error {
	header := "JASC-PAL\n0100\n256\n"
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	for i := 0; i < EntryCount; i++ {
		if _, err := fmt.Fprintf(w, "%d %d %d\n", p.Colors[i].R, p.Colors[i].G, p.Colors[i].B); err != nil {
			return err
		}
	}
	return nil
}

// WriteGPL encodes the palette in the GIMP Palette (.gpl) text format.
func (p *Palette) WriteGPL(w io.Writer, name string) error {
	if name == "" {
		name = "TA Palette"
	}
	if _, err := fmt.Fprintf(w, "GIMP Palette\nName: %s\nColumns: 16\n#\n", name); err != nil {
		return err
	}
	for i := 0; i < EntryCount; i++ {
		if _, err := fmt.Fprintf(w, "%3d %3d %3d\tIndex %d\n",
			p.Colors[i].R, p.Colors[i].G, p.Colors[i].B, i); err != nil {
			return err
		}
	}
	return nil
}

// Histogram returns a summary of how many distinct RGB triples the palette
// contains.  Index 0 is excluded because it is the transparent sentinel.  The
// result is useful when comparing palettes since Cavedog's defaults reserve
// certain ranges for team colors and shadows.
func (p *Palette) Histogram() (unique int, duplicates int) {
	seen := make(map[uint32]int, EntryCount)
	for i := 1; i < EntryCount; i++ {
		c := p.Colors[i]
		key := uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
		seen[key]++
	}
	for _, n := range seen {
		if n > 1 {
			duplicates += n - 1
		}
	}
	return len(seen), duplicates
}

// IsLikelyTAPalette returns true if the file's alpha bytes are all zero, which
// is the case for Cavedog-shipped TA palettes (and a common sanity check).
func (p *Palette) IsLikelyTAPalette() bool {
	if len(p.Raw) != FileSize {
		return false
	}
	for i := 0; i < EntryCount; i++ {
		if p.Raw[i*4+3] != 0 {
			return false
		}
	}
	return true
}

// Equals reports whether two palettes have the same RGB values (alpha is
// ignored because color index 0 always carries the transparent override).
func (p *Palette) Equals(other *Palette) bool {
	if other == nil {
		return false
	}
	for i := 0; i < EntryCount; i++ {
		if p.Colors[i].R != other.Colors[i].R ||
			p.Colors[i].G != other.Colors[i].G ||
			p.Colors[i].B != other.Colors[i].B {
			return false
		}
	}
	return true
}
