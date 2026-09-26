// Package fnt reads Total Annihilation bitmap font (.fnt) files and lays text
// out with them the way the game does.
//
// # File layout
//
// Every offset is measured from the first byte of the font (the header), not
// from the start of any larger stream the font is embedded in.
//
//   - byte 0: glyph height in rows, shared by every glyph. Byte 1 is not part
//     of the height; the game ignores it.
//   - byte 2: baseline, a signed byte. Text drawn with the pen at row y puts
//     the top glyph row at y - baseline.
//   - byte 3: first character code. The offset table starts with this code, so
//     character c uses table entry c - first and codes below it have no glyph.
//   - bytes 4 onwards: 256 - first little-endian uint16 glyph offsets; 0 means
//     the character has no glyph.
//   - each glyph: a width byte (1-255) followed by width × height bits, most
//     significant bit first, packed continuously across rows and padded to a
//     whole byte at the end.
//
// Retail fonts have a first character code of 0 (a full 256-entry table and
// a 516-byte preamble), heights of 9 to 17 rows and baselines of 1 to 3.
//
// # Reading rules
//
// The game reads a glyph only when it draws it and never validates a font, so
// this package keeps every glyph it can decode and skips the rest, recording
// why in [Font.Warnings]: an offset past the end of the font, a width of 0
// (the game advances by 0 and draws garbage) or a bitmap cut short by the end
// of the file. Only a truncated header or offset table, or a height of 0, is
// an error.
//
// An offset that points back into the header or offset table is decoded like
// any other, because that is what the game draws; it only makes sense for a
// font with a shortened table (a non-zero first character code), so it is
// also reported in Warnings.
//
// Offsets are 16-bit, so no glyph can start more than 64 KiB into the font;
// [LoadFromReader] reads at most [MaxFontBytes] bytes.
//
// # Text layout
//
// [Font.MeasureText], [Font.DrawText] and [Font.RenderText] follow the game:
// text is a sequence of raw character bytes (each byte selects the glyph with
// that code, so text must already be in the game's code page; see
// [EncodeCP1252]), drawing stops at the first NUL or newline, each glyph
// advances the pen by exactly its width with no spacing, and a character with
// no glyph draws nothing and advances by 0. The font height is the line
// height.
package fnt

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"unicode/utf8"
)

// headerSize is the size of the fixed header before the offset table.
const headerSize = 4

// maxGlyphBytes is the largest glyph record: a width byte followed by a
// 255 × 255 bitmap.
const maxGlyphBytes = 1 + (255*255+7)/8

// MaxFontBytes is the most bytes a font can address: a glyph record starting
// at the largest 16-bit offset. [LoadFromReader] never reads further.
const MaxFontBytes = 0xFFFF + maxGlyphBytes

// Glyph is a single character's bitmap data.
type Glyph struct {
	Char   int    // Character code (0-255)
	Width  int    // Pixel width, and the pen advance (1-255)
	Height int    // Pixel height (same as font height)
	Pixels []bool // Width × Height pixel values, row by row (true = set)
}

// Font is a parsed FNT file.
type Font struct {
	// Height is the glyph height in rows (header byte 0), which is also the
	// line height.
	Height int
	// Baseline is header byte 2 as a signed value: the number of rows the
	// glyphs extend above the pen position.
	Baseline int
	// FirstChar is header byte 3: the character code of the first offset
	// table entry. Codes below it have no glyph.
	FirstChar int
	// Flags holds header bytes 2 and 3 as a little-endian word.
	//
	// Deprecated: the two bytes are separate fields; use Baseline and
	// FirstChar.
	Flags uint16
	// Glyphs holds the glyph for each character code, nil where the font has
	// none.
	Glyphs [256]*Glyph
	// Warnings lists glyphs that were skipped or that point into the header,
	// one message per glyph.
	Warnings []string
}

// GlyphCount returns the number of defined glyphs.
func (f *Font) GlyphCount() int {
	n := 0
	for _, g := range f.Glyphs {
		if g != nil {
			n++
		}
	}
	return n
}

// LoadFromReader parses an FNT file starting at the reader's current position.
// Glyph offsets are resolved from that position, so a font embedded in a
// larger stream decodes correctly. At most [MaxFontBytes] bytes are read, and
// the reader is left after the last byte read.
func LoadFromReader(r io.ReadSeeker) (*Font, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFontBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read font: %w", err)
	}
	return Parse(data)
}

// Parse decodes an FNT file held in memory. data[0] must be the first header
// byte; bytes past [MaxFontBytes] are never addressed.
func Parse(data []byte) (*Font, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("font header truncated: %d bytes, need %d", len(data), headerSize)
	}
	f := &Font{
		Height:    int(data[0]),
		Baseline:  int(int8(data[2])),
		FirstChar: int(data[3]),
		Flags:     binary.LittleEndian.Uint16(data[2:]),
	}
	if f.Height == 0 {
		return nil, fmt.Errorf("invalid font height: 0")
	}

	entries := 256 - f.FirstChar
	tableEnd := headerSize + 2*entries
	if len(data) < tableEnd {
		return nil, fmt.Errorf("offset table truncated: %d bytes, need %d for %d entries",
			len(data), tableEnd, entries)
	}

	for i := 0; i < entries; i++ {
		ch := f.FirstChar + i
		off := int(binary.LittleEndian.Uint16(data[headerSize+2*i:]))
		if off == 0 {
			continue
		}
		g, warning := decodeGlyph(data, off, ch, f.Height)
		if g != nil && off < tableEnd {
			warning = fmt.Sprintf("glyph 0x%02X: offset %d points into the %d-byte header and offset table",
				ch, off, tableEnd)
		}
		if warning != "" {
			f.Warnings = append(f.Warnings, warning)
		}
		f.Glyphs[ch] = g
	}
	return f, nil
}

// decodeGlyph decodes the glyph record at off. It returns nil and a reason
// when the record cannot be decoded.
func decodeGlyph(data []byte, off, ch, height int) (*Glyph, string) {
	if off >= len(data) {
		return nil, fmt.Sprintf("glyph 0x%02X: offset %d is past the end of the font (%d bytes)", ch, off, len(data))
	}
	w := int(data[off])
	if w == 0 {
		return nil, fmt.Sprintf("glyph 0x%02X: width 0", ch)
	}
	n := (w*height + 7) / 8
	start := off + 1
	if start+n > len(data) {
		return nil, fmt.Sprintf("glyph 0x%02X: bitmap truncated (%d of %d bytes)", ch, len(data)-start, n)
	}
	bits := data[start : start+n]
	pixels := make([]bool, w*height)
	for i := range pixels {
		pixels[i] = bits[i>>3]&(0x80>>uint(i&7)) != 0
	}
	return &Glyph{Char: ch, Width: w, Height: height, Pixels: pixels}, ""
}

// Advance returns how far the pen moves for character byte c: the glyph's
// width, or 0 when the font has no glyph for c.
func (f *Font) Advance(c byte) int {
	if g := f.Glyphs[c]; g != nil {
		return g.Width
	}
	return 0
}

// MeasureText returns the width in pixels the game gives text: the sum of the
// glyph widths of its bytes up to the first NUL or newline. Characters without
// a glyph add nothing.
func (f *Font) MeasureText(text string) int {
	w := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == 0 || c == '\n' {
			break
		}
		w += f.Advance(c)
	}
	return w
}

// DrawText draws text onto dst with the pen at (x, y) as the game does and
// returns the pen advance. Glyph rows start at y - Baseline; set bits are
// painted fg and clear bits bg, or left untouched when bg is nil. Each byte of
// text selects the glyph with that code, and drawing stops at the first NUL or
// newline.
func (f *Font) DrawText(dst draw.Image, x, y int, text string, fg, bg color.Color) int {
	top := y - f.Baseline
	pen := x
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == 0 || c == '\n' {
			break
		}
		g := f.Glyphs[c]
		if g == nil {
			continue
		}
		for row := 0; row < g.Height; row++ {
			for col := 0; col < g.Width; col++ {
				switch {
				case g.Pixels[row*g.Width+col]:
					dst.Set(pen+col, top+row, fg)
				case bg != nil:
					dst.Set(pen+col, top+row, bg)
				}
			}
		}
		pen += g.Width
	}
	return pen - x
}

// RenderImage renders a single glyph as an RGBA image.
func (g *Glyph) RenderImage(fg, bg color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, g.Width, g.Height))
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			if g.Pixels[y*g.Width+x] {
				img.Set(x, y, fg)
			} else {
				img.Set(x, y, bg)
			}
		}
	}
	return img
}

// RenderSheet renders all glyphs as a sprite sheet (16 columns).
func (f *Font) RenderSheet(fg, bg color.Color) *image.RGBA {
	cols := 16
	maxW := 0
	for _, g := range f.Glyphs {
		if g != nil && g.Width > maxW {
			maxW = g.Width
		}
	}
	if maxW == 0 {
		maxW = 8
	}
	cellW := maxW + 2
	cellH := f.Height + 2
	rows := (256 + cols - 1) / cols // 16 rows

	img := image.NewRGBA(image.Rect(0, 0, cols*cellW, rows*cellH))
	// Fill with background.
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, bg)
		}
	}

	for ch := 0; ch < 256; ch++ {
		g := f.Glyphs[ch]
		if g == nil {
			continue
		}
		col := ch % cols
		row := ch / cols
		ox := col*cellW + 1
		oy := row*cellH + 1
		for y := 0; y < g.Height; y++ {
			for x := 0; x < g.Width; x++ {
				if g.Pixels[y*g.Width+x] {
					img.Set(ox+x, oy+y, fg)
				}
			}
		}
	}
	return img
}

// RenderText renders one line of text with the game's layout (see
// [Font.DrawText]) as an image MeasureText(text) pixels wide (at least 1) and
// Height rows tall, the glyph rows filling the image. A nil bg leaves the
// background transparent. Text must be in the game's code page; convert UTF-8
// with [EncodeCP1252] first.
func (f *Font) RenderText(text string, fg, bg color.Color) *image.RGBA {
	w := f.MeasureText(text)
	if w < 1 {
		w = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, w, f.Height))
	if bg != nil {
		draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	}
	f.DrawText(img, 0, f.Baseline, text, fg, bg)
	return img
}

// cp1252High maps the Windows-1252 bytes 0x80-0x9F to their Unicode code
// points; 0 marks the five unassigned bytes.
var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// EncodeCP1252 converts UTF-8 text to Windows-1252, the Western code page of
// the retail game's text, returning one byte per character ready for
// [Font.MeasureText], [Font.DrawText] and [Font.RenderText]. Characters with
// no Windows-1252 byte, and invalid UTF-8, become '?'.
func EncodeCP1252(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		out = append(out, cp1252Byte(r, size))
	}
	return string(out)
}

func cp1252Byte(r rune, size int) byte {
	switch {
	case r == utf8.RuneError && size <= 1:
		return '?'
	case r < 0x80 || (r >= 0xA0 && r <= 0xFF):
		return byte(r)
	}
	for i, c := range cp1252High {
		if c != 0 && c == r {
			return byte(0x80 + i)
		}
	}
	return '?'
}

// WritePNG encodes an image to PNG.
func WritePNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}
