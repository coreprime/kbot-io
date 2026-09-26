package pcx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"

	"github.com/coreprime/kbot-io/formats/gaf"
	"github.com/coreprime/kbot-io/palettes"
)

// Header is the 128-byte PCX file header. TA 3.1c reads only the
// manufacturer, version and extent fields; see the package documentation.
type Header struct {
	Manufacturer byte   // Always 0x0A
	Version      byte   // 5 for 256-colour files; the game loads only version 5
	Encoding     byte   // 1 = RLE encoding; the game always RLE-decodes
	BitsPerPixel byte   // Bits per pixel per plane; the game assumes 8
	XMin         uint16 // Image extent: width is XMax-XMin+1
	YMin         uint16
	XMax         uint16
	YMax         uint16
	HorzDPI      uint16 // Horizontal DPI
	VertDPI      uint16 // Vertical DPI
	Palette      [48]byte
	Reserved     byte
	NumPlanes    byte   // Number of color planes; the game assumes 1
	BytesPerLine uint16 // Bytes per scan line per plane; ignored by the game
	PaletteInfo  uint16 // How to interpret palette (1=color, 2=grayscale)
	HorzScreen   uint16 // Horizontal screen size
	VertScreen   uint16 // Vertical screen size
	Filler       [54]byte
}

const (
	// HeaderSize is the size in bytes of the fixed PCX header.
	HeaderSize = 128
	// ColorMapSize is the size in bytes of the trailing 256-colour map
	// (256 packed RGB triples).
	ColorMapSize = 768
	// PaletteMarker is the byte that precedes the trailing colour map in a
	// standard 256-colour PCX.
	PaletteMarker = 0x0C

	manufacturerZSoft = 0x0A
)

// ErrTruncated reports pixel data that ends before the last row.
var ErrTruncated = errors.New("pcx: pixel data ends before the last row")

// Reader provides methods for reading PCX files. A Reader is not safe for
// concurrent use: each decode records whether its pixel data was truncated.
type Reader struct {
	header    Header
	rawData   []byte // the whole file
	embedded  bool   // a 0x0C marker precedes the last 768 bytes
	truncated bool   // the last decode ran out of pixel data
}

// LoadFromReader reads a whole PCX file from r and parses its header. Only
// the manufacturer byte is checked here.
func LoadFromReader(r io.Reader) (*Reader, error) {
	// Read entire file to support embedded palettes
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read PCX data: %w", err)
	}

	reader := &Reader{rawData: data}

	// Read header
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &reader.header); err != nil {
		return nil, fmt.Errorf("failed to read PCX header: %w", err)
	}

	// Validate header
	if reader.header.Manufacturer != manufacturerZSoft {
		return nil, fmt.Errorf("invalid PCX file: manufacturer byte is 0x%02X (expected 0x0A)", reader.header.Manufacturer)
	}

	// The marker only counts when it lies after the header: on a file too
	// short to hold header, marker and colour map it would be a header or
	// pixel byte.
	if len(data) >= HeaderSize+1+ColorMapSize && data[len(data)-ColorMapSize-1] == PaletteMarker {
		reader.embedded = true
	}

	return reader, nil
}

// Header returns the PCX file header
func (r *Reader) Header() *Header {
	return &r.header
}

// Width returns the image width, XMax-XMin+1, computed in full integers so a
// 65,536-pixel width does not wrap. It is zero or negative when XMax is below
// XMin.
func (r *Reader) Width() int {
	return int(r.header.XMax) - int(r.header.XMin) + 1
}

// Height returns the image height, YMax-YMin+1, computed in full integers. It
// is zero or negative when YMax is below YMin.
func (r *Reader) Height() int {
	return int(r.header.YMax) - int(r.header.YMin) + 1
}

// BitsPerPixel returns the total bits per pixel
func (r *Reader) BitsPerPixel() int {
	return int(r.header.BitsPerPixel) * int(r.header.NumPlanes)
}

// Truncated reports whether the most recent decode ran out of pixel data
// before the last row. An 8-bit decode still succeeds in that case, with the
// missing pixels left at index 0.
func (r *Reader) Truncated() bool {
	return r.truncated
}

// maxImagePixels caps the pixel count of a decoded PCX image so that a
// crafted header cannot force a huge allocation. The largest stock TA PCX
// bitmaps are 640x480 (~307K pixels); this ceiling sits far above any real
// asset yet rejects the pathological allocation.
const maxImagePixels = 64 << 20

// Decode decodes the image as the PCX specification describes; see the
// package documentation.
func (r *Reader) Decode() (image.Image, error) {
	return r.decodeStandard()
}

// checkExtent rejects inverted bounds and images above the pixel cap.
func (r *Reader) checkExtent() error {
	width, height := r.Width(), r.Height()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid PCX dimensions: XMin=%d XMax=%d YMin=%d YMax=%d",
			r.header.XMin, r.header.XMax, r.header.YMin, r.header.YMax)
	}
	if uint64(width)*uint64(height) > maxImagePixels {
		return fmt.Errorf("PCX dimensions %dx%d exceed maximum of %d pixels", width, height, maxImagePixels)
	}
	return nil
}

// standardStride is the number of bytes decoded per row and plane in standard
// mode: BytesPerLine, or the width when BytesPerLine is smaller (which the
// specification does not allow), as the game always decodes width bytes.
func (r *Reader) standardStride() int {
	stride := int(r.header.BytesPerLine)
	if w := r.Width(); stride < w {
		stride = w
	}
	return stride
}

func (r *Reader) decodeStandard() (image.Image, error) {
	r.truncated = false
	if err := r.checkExtent(); err != nil {
		return nil, err
	}
	rect := image.Rect(0, 0, r.Width(), r.Height())

	switch {
	case r.header.BitsPerPixel == 8 && r.header.NumPlanes == 1:
		paletted := image.NewPaletted(rect, r.standardPalette())
		r.decodeRLE8(paletted)
		return paletted, nil
	case r.header.BitsPerPixel == 8 && r.header.NumPlanes == 3:
		rgba := image.NewRGBA(rect)
		if err := r.decodeRLE24(rgba); err != nil {
			return nil, err
		}
		return rgba, nil
	default:
		return nil, fmt.Errorf("unsupported PCX format: %d bits per pixel, %d planes", r.header.BitsPerPixel, r.header.NumPlanes)
	}
}

// opaquePalette converts packed colour entries of the given size (3 for PCX
// RGB triples, 4 for .PAL entries) into a fully opaque 256-colour palette.
func opaquePalette(data []byte, entrySize int) color.Palette {
	pal := make(color.Palette, 256)
	for i := range pal {
		off := i * entrySize
		pal[i] = color.RGBA{R: data[off], G: data[off+1], B: data[off+2], A: 255}
	}
	return pal
}

// standardPalette returns the palette standard mode uses for an 8-bit image:
// the marked colour map, or the built-in TA palette when there is no marker.
// Every entry is opaque.
func (r *Reader) standardPalette() color.Palette {
	if r.embedded {
		return opaquePalette(r.rawData[len(r.rawData)-ColorMapSize:], 3)
	}
	if len(palettes.DefaultPalette) >= 256*4 {
		return opaquePalette(palettes.DefaultPalette, 4)
	}
	pal := make(color.Palette, 256)
	for i := range pal {
		v := uint8(i)
		pal[i] = color.RGBA{v, v, v, 255}
	}
	return pal
}

// decodeRLERow run-length decodes len(dst) bytes from src starting at pos.
// A run that passes the end of dst is clipped and the rest of it dropped; a
// zero-length run consumes its value byte and writes nothing. It returns the
// position after the row and false when src ends first.
func decodeRLERow(src []byte, pos int, dst []byte) (int, bool) {
	x := 0
	for x < len(dst) {
		if pos >= len(src) {
			return pos, false
		}
		b := src[pos]
		pos++
		if b&0xC0 != 0xC0 {
			dst[x] = b
			x++
			continue
		}
		count := int(b & 0x3F)
		if pos >= len(src) {
			return pos, false
		}
		value := src[pos]
		pos++
		if count > len(dst)-x {
			count = len(dst) - x
		}
		for i := 0; i < count; i++ {
			dst[x+i] = value
		}
		x += count
	}
	return pos, true
}

// decodeRLE8 decodes 8-bit single-plane rows in standard mode, recording
// truncation instead of failing.
func (r *Reader) decodeRLE8(img *image.Paletted) {
	width := img.Rect.Dx()
	src := r.rawData[HeaderSize:]
	scanline := make([]byte, r.standardStride())
	pos := 0
	for y := 0; y < img.Rect.Dy(); y++ {
		clear(scanline)
		var ok bool
		pos, ok = decodeRLERow(src, pos, scanline)
		copy(img.Pix[y*img.Stride:y*img.Stride+width], scanline)
		if !ok {
			r.truncated = true
			return
		}
	}
}

// decodeRLE24 decodes 24-bit three-plane rows in standard mode.
func (r *Reader) decodeRLE24(img *image.RGBA) error {
	width := img.Rect.Dx()
	src := r.rawData[HeaderSize:]
	stride := r.standardStride()
	planes := [3][]byte{make([]byte, stride), make([]byte, stride), make([]byte, stride)}
	pos := 0
	for y := 0; y < img.Rect.Dy(); y++ {
		for p, name := range [3]byte{'R', 'G', 'B'} {
			var ok bool
			pos, ok = decodeRLERow(src, pos, planes[p])
			if !ok {
				r.truncated = true
				return fmt.Errorf("failed to decode %c plane at line %d: %w", name, y, ErrTruncated)
			}
		}
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: planes[0][x], G: planes[1][x], B: planes[2][x], A: 255})
		}
	}
	return nil
}

// ConvertToPNG converts a PCX image to PNG format
func ConvertToPNG(w io.Writer, r io.Reader) error {
	reader, err := LoadFromReader(r)
	if err != nil {
		return err
	}

	img, err := reader.Decode()
	if err != nil {
		return err
	}

	return png.Encode(w, img)
}

// ConvertToGIF converts a PCX image to GIF format
func ConvertToGIF(w io.Writer, r io.Reader) error {
	reader, err := LoadFromReader(r)
	if err != nil {
		return err
	}

	img, err := reader.Decode()
	if err != nil {
		return err
	}

	// If already paletted, encode directly
	if paletted, ok := img.(*image.Paletted); ok {
		return gif.Encode(w, paletted, nil)
	}

	// Otherwise convert to paletted with 256-color palette
	bounds := img.Bounds()

	// Create a simple 256-color palette
	palette := make(color.Palette, 256)
	for i := 0; i < 256; i++ {
		palette[i] = color.RGBA{uint8(i), uint8(i), uint8(i), 255}
	}

	paletted := image.NewPaletted(bounds, palette)

	// Copy pixels
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			paletted.Set(x, y, img.At(x, y))
		}
	}

	return gif.Encode(w, paletted, nil)
}

// ConvertToBMP converts a PCX image to BMP format
func ConvertToBMP(w io.Writer, r io.Reader) error {
	reader, err := LoadFromReader(r)
	if err != nil {
		return err
	}

	img, err := reader.Decode()
	if err != nil {
		return err
	}

	// Simple BMP encoding
	return encodeBMP(w, img)
}

// encodeBMP encodes an image as BMP
func encodeBMP(w io.Writer, img image.Image) error {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Calculate row size (must be multiple of 4)
	rowSize := ((width * 3) + 3) & ^3
	imageSize := rowSize * height

	// BMP file header
	fileHeader := []byte{
		'B', 'M', // Signature
		0, 0, 0, 0, // File size (filled below)
		0, 0, 0, 0, // Reserved
		54, 0, 0, 0, // Pixel data offset
	}

	// BMP info header
	infoHeader := []byte{
		40, 0, 0, 0, // Header size
		0, 0, 0, 0, // Width (filled below)
		0, 0, 0, 0, // Height (filled below)
		1, 0, // Planes
		24, 0, // Bits per pixel
		0, 0, 0, 0, // Compression
		0, 0, 0, 0, // Image size (filled below)
		0, 0, 0, 0, // X pixels per meter
		0, 0, 0, 0, // Y pixels per meter
		0, 0, 0, 0, // Colors used
		0, 0, 0, 0, // Important colors
	}

	// Fill in file size
	fileSize := 54 + imageSize
	binary.LittleEndian.PutUint32(fileHeader[2:], uint32(fileSize))

	// Fill in dimensions
	binary.LittleEndian.PutUint32(infoHeader[4:], uint32(width))
	binary.LittleEndian.PutUint32(infoHeader[8:], uint32(height))
	binary.LittleEndian.PutUint32(infoHeader[20:], uint32(imageSize))

	// Write headers
	if _, err := w.Write(fileHeader); err != nil {
		return err
	}
	if _, err := w.Write(infoHeader); err != nil {
		return err
	}

	// Write pixel data (bottom-up, BGR format)
	padding := make([]byte, rowSize-(width*3))

	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Write BGR
			if _, err := w.Write([]byte{byte(b >> 8), byte(g >> 8), byte(r >> 8)}); err != nil {
				return err
			}
		}
		// Write padding
		if len(padding) > 0 {
			if _, err := w.Write(padding); err != nil {
				return err
			}
		}
	}

	return nil
}

// ConvertToGIFWithPalette converts a PCX file to GIF using a custom palette
func ConvertToGIFWithPalette(w io.Writer, r io.Reader, pal *gaf.Palette) error {
	reader, err := LoadFromReader(r)
	if err != nil {
		return err
	}

	img, err := reader.Decode()
	if err != nil {
		return err
	}

	// Convert GAF palette to color.Palette
	palette := make(color.Palette, 256)
	for i := 0; i < 256; i++ {
		palette[i] = pal.Colors[i]
	}

	bounds := img.Bounds()
	paletted := image.NewPaletted(bounds, palette)

	// Copy pixels (index mapping)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			paletted.Set(x, y, img.At(x, y))
		}
	}

	return gif.Encode(w, paletted, nil)
}

// HasEmbeddedPalette reports whether a 0x0C marker precedes the last 768
// bytes, which is how the PCX specification marks a 256-colour map.
func (r *Reader) HasEmbeddedPalette() bool {
	return r.embedded
}

// EmbeddedPalette returns the marked 256-colour map as a *gaf.Palette for
// drawing sprites, or nil when the file has no 0x0C marker before its last 768
// bytes.
//
// The entries follow the gaf package's palette convention (the same result as
// gaf.LoadPaletteFromBytes on the colours), including its treatment of index
// 0. For drawing the image itself use the decoded image's palette, which is
// opaque.
//
// TA: Kingdoms uses sidecar PCX files (often 1x1 px) purely as palette
// containers next to .gaf files, so this is the canonical way to fish the
// palette out without re-decoding the image data.
func (r *Reader) EmbeddedPalette() *gaf.Palette {
	if !r.embedded {
		return nil
	}
	src := r.rawData[len(r.rawData)-ColorMapSize:]
	entries := make([]byte, 256*4)
	for i := 0; i < 256; i++ {
		copy(entries[i*4:i*4+3], src[i*3:i*3+3])
	}
	p, err := gaf.LoadPaletteFromBytes(entries)
	if err != nil {
		return nil
	}
	return p
}
