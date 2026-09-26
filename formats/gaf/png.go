package gaf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
)

var pngSignature = []byte{137, 80, 78, 71, 13, 10, 26, 10}

// ToPNG converts a single frame to PNG with the game's transparency rule.
func (f *Frame) ToPNG(palette *Palette, w io.Writer) error {
	return f.ToPNGWith(palette, RenderOptions{}, w)
}

// ToPNGWith converts a single frame to an indexed PNG with explicit
// transparency options. Pixels keep their palette index (see ToImageWith);
// the PLTE chunk holds the palette's colours and a tRNS chunk marks the one
// transparent slot. Nothing is written if encoding fails.
func (f *Frame) ToPNGWith(palette *Palette, opts RenderOptions, w io.Writer) error {
	img, ep := f.render(palette, opts)
	idat, err := encodeIDAT(img, png.DefaultCompression)
	if err != nil {
		return err
	}

	var out bytes.Buffer
	out.Write(pngSignature)
	writeChunk(&out, "IHDR", ihdr(int(f.Width), int(f.Height)))
	writeChunk(&out, "PLTE", plte(ep))
	if t := trns(ep); t != nil {
		writeChunk(&out, "tRNS", t)
	}
	writeChunk(&out, "IDAT", idat)
	writeChunk(&out, "IEND", nil)
	_, err = w.Write(out.Bytes())
	return err
}

// ToAPNG converts a sequence to an animated PNG (APNG) using the game's
// transparency rule.
func (s *Sequence) ToAPNG(palette *Palette, w io.Writer) error {
	return s.ToAPNGWith(palette, RenderOptions{}, w)
}

// ToAPNGWith converts a sequence to an animated PNG (APNG) with explicit
// transparency options. Frames are placed as in ToGIFWith. A one-frame
// sequence is written as a still PNG of that frame. Nothing is written if
// encoding fails.
func (s *Sequence) ToAPNGWith(palette *Palette, opts RenderOptions, w io.Writer) error {
	if len(s.Frames) == 0 {
		return fmt.Errorf("no frames in sequence")
	}

	// For single frame, just write PNG with the same options.
	if len(s.Frames) == 1 && s.Frames[0] != nil {
		return s.Frames[0].ToPNGWith(palette, opts, w)
	}

	sc, err := s.renderCanvases(palette, opts)
	if err != nil {
		return err
	}

	var out bytes.Buffer
	out.Write(pngSignature)
	writeChunk(&out, "IHDR", ihdr(sc.width, sc.height))

	// acTL (animation control) must come before IDAT.
	actl := make([]byte, 8)
	binary.BigEndian.PutUint32(actl[0:], uint32(len(sc.images))) // num_frames
	binary.BigEndian.PutUint32(actl[4:], 0)                      // num_plays (0 = infinite)
	writeChunk(&out, "acTL", actl)

	writeChunk(&out, "PLTE", plte(sc.palette))
	if t := trns(sc.palette); t != nil {
		writeChunk(&out, "tRNS", t)
	}

	sequenceNumber := uint32(0)
	for frameIdx, canvas := range sc.images {
		frame := s.Frames[frameIdx]
		delay := uint16((frame.Duration * 100) / 30)
		if delay == 0 {
			delay = 10 // Default ~100ms (10/100 = 0.1s)
		}

		// fcTL (frame control): every frame covers the whole canvas.
		fctl := make([]byte, 26)
		binary.BigEndian.PutUint32(fctl[0:], sequenceNumber)
		binary.BigEndian.PutUint32(fctl[4:], uint32(sc.width))
		binary.BigEndian.PutUint32(fctl[8:], uint32(sc.height))
		binary.BigEndian.PutUint32(fctl[12:], 0) // x_offset
		binary.BigEndian.PutUint32(fctl[16:], 0) // y_offset
		binary.BigEndian.PutUint16(fctl[20:], delay)
		binary.BigEndian.PutUint16(fctl[22:], 100)
		fctl[24] = 1 // dispose_op: APNG_DISPOSE_OP_BACKGROUND
		fctl[25] = 0 // blend_op: APNG_BLEND_OP_SOURCE (replace)
		writeChunk(&out, "fcTL", fctl)
		sequenceNumber++

		idat, err := encodeIDAT(canvas, png.NoCompression)
		if err != nil {
			return fmt.Errorf("frame %d: %w", frameIdx, err)
		}
		// First frame uses IDAT, subsequent frames use fdAT.
		if frameIdx == 0 {
			writeChunk(&out, "IDAT", idat)
		} else {
			fdat := make([]byte, 4, 4+len(idat))
			binary.BigEndian.PutUint32(fdat, sequenceNumber)
			writeChunk(&out, "fdAT", append(fdat, idat...))
			sequenceNumber++
		}
	}

	writeChunk(&out, "IEND", nil)
	_, err = w.Write(out.Bytes())
	return err
}

// ihdr returns an IHDR payload for an 8-bit indexed image.
func ihdr(width, height int) []byte {
	b := make([]byte, 13)
	binary.BigEndian.PutUint32(b[0:], uint32(width))
	binary.BigEndian.PutUint32(b[4:], uint32(height))
	b[8] = 8  // bit depth
	b[9] = 3  // colour type: indexed
	b[10] = 0 // compression
	b[11] = 0 // filter
	b[12] = 0 // interlace
	return b
}

// plte returns the PLTE payload: the palette's own colours, including the
// colour behind the transparent slot.
func plte(ep exportPalette) []byte {
	b := make([]byte, 0, 3*len(ep.rgb))
	for _, c := range ep.rgb {
		r, g, bl, _ := c.RGBA()
		b = append(b, byte(r>>8), byte(g>>8), byte(bl>>8))
	}
	return b
}

// trns returns the tRNS payload marking the transparent slot, or nil when
// the export has none.
func trns(ep exportPalette) []byte {
	if !ep.hasSlot {
		return nil
	}
	b := make([]byte, len(ep.rgb))
	for i := range b {
		b[i] = 255
	}
	b[ep.slot] = 0
	return b
}

// encodeIDAT encodes img with the standard PNG encoder and returns its image
// data (the concatenated IDAT payloads).
func encodeIDAT(img *image.Paletted, level png.CompressionLevel) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: level}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return extractAllIDAT(buf.Bytes())
}

// writeChunk appends a PNG chunk with length, type, data, and CRC.
func writeChunk(out *bytes.Buffer, chunkType string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	out.Write(n[:])
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(chunkType))
	_, _ = crc.Write(data)
	out.WriteString(chunkType)
	out.Write(data)
	binary.BigEndian.PutUint32(n[:], crc.Sum32())
	out.Write(n[:])
}

// extractAllIDAT concatenates every IDAT chunk payload from a complete PNG
// byte stream. It walks the chunk structure (length, type, payload, CRC)
// starting after the 8-byte signature rather than scanning for the literal
// "IDAT" bytes, which could otherwise false-match on palette or other chunk
// data and read a bogus length.
func extractAllIDAT(pngData []byte) ([]byte, error) {
	var idatData []byte
	pos := 8 // skip the PNG signature
	for pos+8 <= len(pngData) {
		length := binary.BigEndian.Uint32(pngData[pos:])
		chunkType := string(pngData[pos+4 : pos+8])
		start := pos + 8
		end := start + int(length)
		if end > len(pngData) {
			break
		}
		if chunkType == "IDAT" {
			idatData = append(idatData, pngData[start:end]...)
		}
		pos = end + 4 // skip the 4-byte CRC
	}

	if len(idatData) == 0 {
		return nil, fmt.Errorf("no IDAT chunks found")
	}

	return idatData, nil
}
