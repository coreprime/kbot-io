package pcx

import (
	"fmt"
	"image"
	"image/color"
)

// GamePalette returns the palette TA 3.1c uses for this file: the last 768
// bytes, whether or not a 0x0C marker precedes them, with every entry opaque.
// It returns nil when the file is shorter than MinGameFileSize, which the game
// refuses.
func (r *Reader) GamePalette() color.Palette {
	if len(r.rawData) < MinGameFileSize {
		return nil
	}
	return opaquePalette(r.rawData[len(r.rawData)-ColorMapSize:], 3)
}

// gameLoadable returns an error wrapping ErrGameRejects when TA 3.1c refuses
// the file, or the pixel-cap error for images too large to decode here.
func (r *Reader) gameLoadable() error {
	if r.header.Version != GameVersion {
		return fmt.Errorf("%w: version %d (only version %d is loaded)", ErrGameRejects, r.header.Version, GameVersion)
	}
	if r.Width() <= 0 || r.Height() <= 0 {
		return fmt.Errorf("%w: bounds XMin=%d XMax=%d YMin=%d YMax=%d are inverted",
			ErrGameRejects, r.header.XMin, r.header.XMax, r.header.YMin, r.header.YMax)
	}
	if len(r.rawData) < MinGameFileSize {
		return fmt.Errorf("%w: %d bytes cannot hold the %d-byte header and the %d-byte colour map",
			ErrGameRejects, len(r.rawData), HeaderSize, ColorMapSize)
	}
	return r.checkExtent()
}

// DecodeGame decodes the image as TA 3.1c does (ModeGame): version 5 is
// required; encoding, depth, planes and BytesPerLine are ignored; each row is
// width pixels of run-length data read straight after the header; and the
// palette is the last 768 bytes, opaque, whatever precedes them. Pixel data
// that ends early is not an error: the missing pixels stay at index 0 and
// Truncated reports it.
//
// Files the game refuses fail with an error wrapping ErrGameRejects.
func (r *Reader) DecodeGame() (*image.Paletted, error) {
	r.truncated = false
	if err := r.gameLoadable(); err != nil {
		return nil, err
	}
	width, height := r.Width(), r.Height()
	img := image.NewPaletted(image.Rect(0, 0, width, height), r.GamePalette())
	src := r.rawData[HeaderSize:]
	pos := 0
	for y := 0; y < height; y++ {
		var ok bool
		pos, ok = decodeRLERow(src, pos, img.Pix[y*img.Stride:y*img.Stride+width])
		if !ok {
			r.truncated = true
			break
		}
	}
	return img, nil
}

// gameRowsEnd scans the rows as the game decodes them without keeping the
// pixels. It returns the file offset after the last row and whether the data
// held every row.
func (r *Reader) gameRowsEnd() (int, bool) {
	row := make([]byte, r.Width())
	src := r.rawData[HeaderSize:]
	pos := 0
	for y := 0; y < r.Height(); y++ {
		var ok bool
		if pos, ok = decodeRLERow(src, pos, row); !ok {
			return HeaderSize + pos, false
		}
	}
	return HeaderSize + pos, true
}
