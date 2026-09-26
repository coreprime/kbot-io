package gaf

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// WriteOptions controls WriteGAFWith.
type WriteOptions struct {
	// DefaultStorage is the storage for frames whose Storage is
	// StorageDefault; StorageDefault here means compressed. Archives the
	// game reads as plain pixel arrays need StorageRaw (see StorageForPath).
	DefaultStorage FrameStorage
	// FlattenLayers writes composite frames as simple frames from their
	// Pixels (and Opaque) instead of writing their Layers.
	FlattenLayers bool
}

// WriteGAF writes a complete GAF file containing the given sequences, with
// default options: frames without an explicit Storage are compressed.
// Frame pixel data must already be palette indices ([]byte, one byte per
// pixel).
func WriteGAF(w io.Writer, sequences []*Sequence) error {
	return WriteGAFWith(w, sequences, WriteOptions{})
}

// WriteGAFWith writes a complete GAF file containing the given sequences.
//
// Every field the reader keeps is written back: each sequence's LoopFlags
// and Unknown4, and each frame's storage, Blend byte, Unknown12, Unknown20
// and Layers. A frame is stored raw or compressed as its Storage says, or as
// opts.DefaultStorage when it is StorageDefault. A compressed frame encodes
// the pixels that are not opaque (see Frame.PixelOpaque) as skips, so a
// key-valued pixel marked opaque in Opaque stays opaque; a raw frame stores
// Pixels as they are, and the game treats every key-valued pixel of a raw
// frame as transparent. Every frame gets its own header, as the game cannot
// load files that share frame headers.
//
// Nothing is written when the input cannot be stored faithfully: more than
// MaxSequences sequences or MaxFramesPerSequence frames, a name longer than
// MaxNameLength bytes, a duration above MaxDuration, more than MaxLayers
// layers, a layer that has layers of its own, a pixel or Opaque buffer whose
// length is not Width*Height, a compressed row longer than 65535 bytes, a nil
// sequence or frame, or a file larger than 4 GiB.
func WriteGAFWith(w io.Writer, sequences []*Sequence, opts WriteOptions) error {
	buf, err := encodeGAF(sequences, opts)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

type encoder struct {
	buf  []byte
	opts WriteOptions
}

// offset returns the current length as a file offset.
func (e *encoder) offset() (uint32, error) {
	if uint64(len(e.buf)) > math.MaxUint32 {
		return 0, fmt.Errorf("GAF data exceeds 4 GiB")
	}
	return uint32(len(e.buf)), nil
}

func (e *encoder) put32(at int, v uint32) {
	binary.LittleEndian.PutUint32(e.buf[at:], v)
}

func encodeGAF(sequences []*Sequence, opts WriteOptions) ([]byte, error) {
	if len(sequences) > MaxSequences {
		return nil, fmt.Errorf("%d sequences exceed the GAF limit of %d", len(sequences), MaxSequences)
	}
	e := &encoder{opts: opts}

	// ── header (12 bytes) ──────────────────────────────────────────────
	e.buf = appendLE(e.buf, Header{Version: VersionTA, SequenceCount: uint32(len(sequences))})

	// ── sequence pointer table ─────────────────────────────────────────
	// Placeholder — filled in after we know each sequence's offset.
	ptrTableOffset := len(e.buf)
	e.buf = append(e.buf, make([]byte, 4*len(sequences))...)

	// ── per-sequence data ──────────────────────────────────────────────
	for si, seq := range sequences {
		if err := e.writeSequence(seq, ptrTableOffset+4*si); err != nil {
			name := ""
			if seq != nil {
				name = seq.Name
			}
			return nil, fmt.Errorf("sequence %d (%q): %w", si, name, err)
		}
	}
	if _, err := e.offset(); err != nil {
		return nil, err
	}
	return e.buf, nil
}

func (e *encoder) writeSequence(seq *Sequence, pointerAt int) error {
	if seq == nil {
		return fmt.Errorf("sequence is nil")
	}
	if len(seq.Name) > MaxNameLength {
		return fmt.Errorf("name is %d bytes; the name field holds %d", len(seq.Name), MaxNameLength)
	}
	if len(seq.Frames) > MaxFramesPerSequence {
		return fmt.Errorf("%d frames exceed the limit of %d", len(seq.Frames), MaxFramesPerSequence)
	}
	seqOffset, err := e.offset()
	if err != nil {
		return err
	}
	e.put32(pointerAt, seqOffset)

	// Sequence header (40 bytes).
	sh := SequenceHeader{
		FrameCount: uint16(len(seq.Frames)),
		Unknown1:   seq.LoopFlags,
		Unknown2:   seq.Unknown4,
	}
	copy(sh.Name[:], seq.Name)
	e.buf = appendLE(e.buf, sh)

	// Frame list items — placeholders, patched below.
	frameListStart := len(e.buf)
	e.buf = append(e.buf, make([]byte, 8*len(seq.Frames))...)

	for fi, frame := range seq.Frames {
		if frame == nil {
			return fmt.Errorf("frame %d is nil", fi)
		}
		if frame.Duration > MaxDuration {
			return fmt.Errorf("frame %d: duration %d exceeds %d ticks", fi, frame.Duration, MaxDuration)
		}
		infoOffset, err := e.writeFrame(frame, false)
		if err != nil {
			return fmt.Errorf("frame %d: %w", fi, err)
		}
		item := frameListStart + fi*8
		e.put32(item, infoOffset)
		e.put32(item+4, frame.Duration)
	}
	return nil
}

// storageFor resolves a frame's storage against the options.
func (e *encoder) storageFor(f *Frame) FrameStorage {
	s := f.Storage
	if s == StorageDefault {
		s = e.opts.DefaultStorage
	}
	if s == StorageRaw {
		return StorageRaw
	}
	return StorageCompressed
}

// writeFrame appends a frame header and its data (or its layers) and returns
// the header's offset.
func (e *encoder) writeFrame(f *Frame, asLayer bool) (uint32, error) {
	storage := e.storageFor(f)
	info := FrameInfo{
		Width:             f.Width,
		Height:            f.Height,
		OriginX:           f.OriginX,
		OriginY:           f.OriginY,
		TransparencyIndex: f.TransparencyIndex,
		Blend:             f.Blend,
		Unknown2:          f.Unknown12,
		Unknown3:          f.Unknown20,
	}
	if storage == StorageCompressed {
		info.Compressed = 1
	}

	infoOffset, err := e.offset()
	if err != nil {
		return 0, err
	}
	infoStart := len(e.buf)

	if len(f.Layers) > 0 && !e.opts.FlattenLayers {
		if asLayer {
			return 0, fmt.Errorf("a layer has layers of its own; the game does not draw nested layers")
		}
		if len(f.Layers) > MaxLayers {
			return 0, fmt.Errorf("%d layers exceed the limit of %d", len(f.Layers), MaxLayers)
		}
		info.LayerCount = uint8(len(f.Layers))
		e.buf = appendLE(e.buf, info)
		tableOffset, err := e.offset()
		if err != nil {
			return 0, err
		}
		e.put32(infoStart+16, tableOffset)
		tableStart := len(e.buf)
		e.buf = append(e.buf, make([]byte, 4*len(f.Layers))...)
		for li, layer := range f.Layers {
			if layer == nil {
				return 0, fmt.Errorf("layer %d is nil", li)
			}
			layerOffset, err := e.writeFrame(layer, true)
			if err != nil {
				return 0, fmt.Errorf("layer %d: %w", li, err)
			}
			e.put32(tableStart+4*li, layerOffset)
		}
		return infoOffset, nil
	}

	size := int(f.Width) * int(f.Height)
	if len(f.Pixels) != size {
		return 0, fmt.Errorf("%dx%d frame has %d pixels, want %d", f.Width, f.Height, len(f.Pixels), size)
	}
	if f.Opaque != nil && len(f.Opaque) != size {
		return 0, fmt.Errorf("%dx%d frame has %d Opaque entries, want %d", f.Width, f.Height, len(f.Opaque), size)
	}

	var data []byte
	if storage == StorageCompressed {
		if data, err = compressFrame(f); err != nil {
			return 0, err
		}
	} else {
		data = f.Pixels
	}

	e.buf = appendLE(e.buf, info)
	dataOffset, err := e.offset()
	if err != nil {
		return 0, err
	}
	// FrameInfo layout: Width(2)+Height(2)+OriginX(2)+OriginY(2)+
	//   TranspIdx(1)+Compressed(1)+LayerCount(1)+Blend(1)+Unknown2(4) = offset 16
	e.put32(infoStart+16, dataOffset)
	e.buf = append(e.buf, data...)
	return infoOffset, nil
}

// compressFrame compresses pixel data using the GAF row-based compression.
func compressFrame(f *Frame) ([]byte, error) {
	var out []byte
	w := int(f.Width)
	h := int(f.Height)
	opaque := make([]bool, w)

	for row := 0; row < h; row++ {
		rowStart := row * w
		rowPixels := f.Pixels[rowStart : rowStart+w]
		if f.Opaque != nil {
			copy(opaque, f.Opaque[rowStart:rowStart+w])
		} else {
			for i, p := range rowPixels {
				opaque[i] = p != f.TransparencyIndex
			}
		}

		compressed := compressRow(rowPixels, opaque)
		if len(compressed) > math.MaxUint16 {
			return nil, fmt.Errorf("row %d compresses to %d bytes; a row holds at most %d", row, len(compressed), math.MaxUint16)
		}

		// Row size prefix (2 bytes little-endian).
		out = append(out, byte(len(compressed)), byte(len(compressed)>>8))
		out = append(out, compressed...)
	}

	return out, nil
}

// compressRow compresses a single row using the three GAF encoding modes:
//
//	mask & 0x01: skip (transparent) — mask>>1 = count
//	mask & 0x02: repeat — mask>>2 + 1 = count, followed by 1 byte value
//	else:        literal — mask>>2 + 1 = count, followed by N bytes
//
// opaque[i] says whether pixel i is drawn; the others become skips.
func compressRow(pixels []byte, opaque []bool) []byte {
	var out []byte
	n := len(pixels)
	i := 0

	for i < n {
		// Transparent run?
		if !opaque[i] {
			count := 0
			for i+count < n && !opaque[i+count] && count < 127 {
				count++
			}
			out = append(out, byte((count<<1)|0x01))
			i += count
			continue
		}

		// RLE: current pixel repeats?
		runLen := 1
		for i+runLen < n && opaque[i+runLen] && pixels[i+runLen] == pixels[i] && runLen < 63 {
			runLen++
		}

		if runLen >= 3 {
			out = append(out, byte(((runLen-1)<<2)|0x02), pixels[i])
			i += runLen
			continue
		}

		// Literal run: collect non-repeating opaque pixels.
		litStart := i
		for i < n && (i-litStart) < 63 {
			if !opaque[i] {
				break
			}
			// Check if an RLE run of 3+ starts here.
			if i+2 < n && opaque[i+1] && opaque[i+2] && pixels[i] == pixels[i+1] && pixels[i] == pixels[i+2] {
				break
			}
			i++
		}
		count := i - litStart
		out = append(out, byte((count-1)<<2))
		out = append(out, pixels[litStart:litStart+count]...)
	}

	return out
}

// ── helpers ────────────────────────────────────────────────────────────────

func appendLE(buf []byte, v any) []byte {
	size := binary.Size(v)
	b := make([]byte, size)
	// Use a temporary writer to serialize.
	w := &sliceWriter{buf: b}
	_ = binary.Write(w, binary.LittleEndian, v)
	return append(buf, b...)
}

type sliceWriter struct {
	buf []byte
	pos int
}

func (sw *sliceWriter) Write(p []byte) (int, error) {
	n := copy(sw.buf[sw.pos:], p)
	sw.pos += n
	if n < len(p) {
		return n, fmt.Errorf("buffer overflow")
	}
	return n, nil
}
