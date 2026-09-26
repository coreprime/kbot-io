package gaf

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"
)

// maxFramePixels caps the pixel count of a single decoded frame. Width and
// height are attacker-controlled uint16 fields, so a crafted header could ask
// for up to 65535x65535 (~4 GB) and force an out-of-memory allocation before
// any read fails. The largest stock TA/TA:K GAF frames are 640x480 interface
// art (~307K pixels); this ceiling sits far above any real asset yet rejects
// the pathological allocation.
const maxFramePixels = 64 << 20

// maxDecodedBytes caps the frame data one ReadSequences call may read and
// produce in total: the pixel, mask and canvas bytes it allocates plus the
// compressed row bytes (size words included) it reads and scans. Frame
// headers, and the row data behind them, can be referenced from many places,
// so a small file could otherwise ask for terabytes of memory or hours of
// decoding. The largest stock file (anims/ur-buildings1.gaf) needs about
// 123 MiB.
const maxDecodedBytes = 512 << 20

// maxFrameRecords caps the number of frame headers (including layers) one
// ReadSequences call visits. The largest stock file has about 5,200.
const maxFrameRecords = 1 << 20

// maxWarnings caps the warnings kept per read; further ones are counted.
const maxWarnings = 64

// frameByteCount validates frame dimensions and returns the pixel-buffer size.
func frameByteCount(width, height uint16) (int, error) {
	size := uint64(width) * uint64(height)
	if size > maxFramePixels {
		return 0, fmt.Errorf("frame dimensions %dx%d exceed maximum of %d pixels", width, height, maxFramePixels)
	}
	return int(size), nil
}

// Warning is a non-fatal irregularity found while reading a GAF: something
// the reader accepted but that the game reads differently, cannot load, or
// that no stock file contains.
type Warning struct {
	Offset  int64  // File offset of the record concerned
	Message string // What was found and how it was handled
}

// String formats the warning as "0xOFFSET: message".
func (w Warning) String() string {
	return fmt.Sprintf("0x%X: %s", w.Offset, w.Message)
}

// Reader reads GAF files.
type Reader struct {
	file           io.ReadSeeker
	header         Header
	headerWarnings []Warning
	warnings       []Warning
}

// LoadFromReader creates a new Reader from an io.ReadSeeker and reads the GAF
// header. Any version word is accepted, as the game does; an unfamiliar one
// is reported by Warnings.
func LoadFromReader(rs io.ReadSeeker) (*Reader, error) {
	r := &Reader{file: rs}
	if err := r.readHeader(); err != nil {
		return nil, err
	}
	return r, nil
}

// LoadFromFile opens a GAF file at path and reads its header.
func LoadFromFile(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{file: file}
	if err := r.readHeader(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) readHeader() error {
	if err := binary.Read(r.file, binary.LittleEndian, &r.header); err != nil {
		return fmt.Errorf("failed to read GAF header: %w", err)
	}
	if !isKnownGAFVersion(r.header.Version) {
		r.headerWarnings = append(r.headerWarnings, Warning{
			Offset:  0,
			Message: fmt.Sprintf("unfamiliar GAF version word 0x%08X (stock files use 0x%08X or 0); the game ignores it", r.header.Version, uint32(VersionTA)),
		})
	}
	count := r.header.SequenceCount
	switch {
	case int16(uint16(count)) < 0:
		r.headerWarnings = append(r.headerWarnings, Warning{
			Offset:  4,
			Message: fmt.Sprintf("sequence count word 0x%08X is negative as a 16-bit value; the game reads no sequences", count),
		})
	case count>>16 != 0:
		r.headerWarnings = append(r.headerWarnings, Warning{
			Offset:  4,
			Message: fmt.Sprintf("sequence count word 0x%08X has high bits set; the game reads only the low 16 bits (%d sequences)", count, r.header.EffectiveSequenceCount()),
		})
	}
	return nil
}

// Close closes the reader if its underlying source is an io.Closer.
func (r *Reader) Close() error {
	if r.file != nil {
		if closer, ok := r.file.(io.Closer); ok {
			return closer.Close()
		}
	}
	return nil
}

// Header returns the file header.
func (r *Reader) Header() *Header {
	return &r.header
}

// Warnings returns the irregularities found in the header and by the most
// recent ReadSequences call.
func (r *Reader) Warnings() []Warning {
	out := make([]Warning, 0, len(r.headerWarnings)+len(r.warnings))
	out = append(out, r.headerWarnings...)
	return append(out, r.warnings...)
}

// ReadSequences reads all animation sequences from the file.
//
// It follows the game's reading rules: the sequence count is the signed low
// 16 bits of the header word, frame durations are the low 16 bits of their
// word, frame byte +10 is the layer count and byte +11 a separate flag. The
// storage, flags and unknown words of every sequence and frame are kept so
// WriteGAF can write them back. Layers that are themselves composites are
// skipped and reported by Warnings, as are shared frame headers and
// compressed rows that end before the frame width. A file is rejected when it
// has more than 2^20 frame headers, or when the pixel data its frames decode
// to plus the compressed row bytes read for them (counted once per frame
// header) exceed 512 MiB.
func (r *Reader) ReadSequences() ([]*Sequence, error) {
	d := newDecoder(r.file)
	sequences, err := d.readSequences(r.header)
	d.finishWarnings()
	r.warnings = d.warnings
	if err != nil {
		return nil, err
	}
	return sequences, nil
}

// decoder holds the state of one ReadSequences call.
type decoder struct {
	file       io.ReadSeeker
	budget     int64             // bytes still allowed to be read or produced
	records    int               // frame headers visited
	cache      map[uint32]*Frame // decoded frames by header offset
	refs       map[uint32]int    // references to each frame header
	warnings   []Warning
	suppressed int
	rowBuf     []byte
}

func newDecoder(file io.ReadSeeker) *decoder {
	return &decoder{
		file:   file,
		budget: maxDecodedBytes,
		cache:  make(map[uint32]*Frame),
		refs:   make(map[uint32]int),
	}
}

func (d *decoder) warn(offset int64, format string, args ...any) {
	if len(d.warnings) >= maxWarnings {
		d.suppressed++
		return
	}
	d.warnings = append(d.warnings, Warning{Offset: offset, Message: fmt.Sprintf(format, args...)})
}

func (d *decoder) finishWarnings() {
	if d.suppressed > 0 {
		d.warnings = append(d.warnings, Warning{Offset: -1, Message: fmt.Sprintf("%d further warnings not shown", d.suppressed)})
	}
}

// charge takes n bytes from the decode budget.
func (d *decoder) charge(n int) error {
	if int64(n) > d.budget {
		return fmt.Errorf("frame data read and decoded exceeds the %d MiB limit (frame headers or row data referenced too often, or frames too large)", maxDecodedBytes>>20)
	}
	d.budget -= int64(n)
	return nil
}

func (d *decoder) readAt(offset int64, buf []byte) error {
	if _, err := d.file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	_, err := io.ReadFull(d.file, buf)
	return err
}

func (d *decoder) readSequences(h Header) ([]*Sequence, error) {
	count := h.EffectiveSequenceCount()
	table := make([]byte, 4*count)
	if err := d.readAt(12, table); err != nil {
		return nil, fmt.Errorf("failed to read sequence pointer table (%d entries): %w", count, err)
	}
	sequences := make([]*Sequence, 0, count)
	for i := 0; i < count; i++ {
		ptr := binary.LittleEndian.Uint32(table[4*i:])
		seq, err := d.readSequence(ptr)
		if err != nil {
			return nil, fmt.Errorf("failed to read sequence %d at offset 0x%X: %w", i, ptr, err)
		}
		sequences = append(sequences, seq)
	}
	return sequences, nil
}

// readSequence reads a single sequence at the given offset.
func (d *decoder) readSequence(offset uint32) (*Sequence, error) {
	if _, err := d.file.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, err
	}
	var sh SequenceHeader
	if err := binary.Read(d.file, binary.LittleEndian, &sh); err != nil {
		return nil, fmt.Errorf("failed to read sequence header: %w", err)
	}
	if d.records+int(sh.FrameCount) > maxFrameRecords {
		return nil, fmt.Errorf("more than %d frame headers in the file", maxFrameRecords)
	}

	list := make([]byte, 8*int(sh.FrameCount))
	if _, err := io.ReadFull(d.file, list); err != nil {
		return nil, fmt.Errorf("failed to read frame list (%d entries): %w", sh.FrameCount, err)
	}

	seq := &Sequence{
		Name:      nullTerminatedString(sh.Name[:]),
		Frames:    make([]*Frame, 0, sh.FrameCount),
		LoopFlags: sh.Unknown1,
		Unknown4:  sh.Unknown2,
	}
	for i := 0; i < int(sh.FrameCount); i++ {
		ptr := binary.LittleEndian.Uint32(list[8*i:])
		duration := binary.LittleEndian.Uint32(list[8*i+4:])
		if duration > MaxDuration {
			d.warn(int64(offset)+40+int64(8*i)+4, "duration word 0x%08X of frame %d has high bits set; the game reads only the low 16 bits (%d ticks)", duration, i, duration&0xFFFF)
			duration &= 0xFFFF
		}
		frame, err := d.readFrame(ptr, false)
		if err != nil {
			return nil, fmt.Errorf("failed to read frame %d at offset 0x%X: %w", i, ptr, err)
		}
		frame.Duration = duration
		seq.Frames = append(seq.Frames, frame)
	}
	return seq, nil
}

// readFrame returns the frame whose header is at offset. A layer (asLayer)
// that is itself a composite yields (nil, nil) after a warning.
func (d *decoder) readFrame(offset uint32, asLayer bool) (*Frame, error) {
	d.records++
	if d.records > maxFrameRecords {
		return nil, fmt.Errorf("more than %d frame headers in the file", maxFrameRecords)
	}
	d.refs[offset]++
	if d.refs[offset] == 2 {
		d.warn(int64(offset), "frame header is referenced more than once; the game cannot load a file that shares frame headers")
	}
	if cached, ok := d.cache[offset]; ok {
		if asLayer && len(cached.Layers) > 0 {
			d.warn(int64(offset), "layer is itself a composite frame; the game does not draw nested layers, so it was skipped")
			return nil, nil
		}
		return d.clone(cached)
	}

	if _, err := d.file.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, err
	}
	var fi FrameInfo
	if err := binary.Read(d.file, binary.LittleEndian, &fi); err != nil {
		return nil, fmt.Errorf("failed to read frame info: %w", err)
	}

	var (
		frame *Frame
		err   error
	)
	if fi.LayerCount > 0 {
		if asLayer {
			d.warn(int64(offset), "layer is itself a composite frame; the game does not draw nested layers, so it was skipped")
			return nil, nil
		}
		frame, err = d.readLayeredFrame(offset, &fi)
	} else {
		frame, err = d.readSimpleFrame(offset, &fi)
	}
	if err != nil {
		return nil, err
	}
	d.cache[offset] = frame
	return frame, nil
}

// clone copies a decoded frame for another reference to the same header.
func (d *decoder) clone(f *Frame) (*Frame, error) {
	if err := d.charge(len(f.Pixels) + len(f.Opaque)); err != nil {
		return nil, err
	}
	c := *f
	c.Pixels = slices.Clone(f.Pixels)
	c.Opaque = slices.Clone(f.Opaque)
	if f.Layers != nil {
		c.Layers = make([]*Frame, len(f.Layers))
		for i, l := range f.Layers {
			lc, err := d.clone(l)
			if err != nil {
				return nil, err
			}
			c.Layers[i] = lc
		}
	}
	return &c, nil
}

func storageOf(compressed uint8) FrameStorage {
	if compressed != 0 {
		return StorageCompressed
	}
	return StorageRaw
}

func newFrame(fi *FrameInfo) *Frame {
	return &Frame{
		Width:             fi.Width,
		Height:            fi.Height,
		OriginX:           fi.OriginX,
		OriginY:           fi.OriginY,
		TransparencyIndex: fi.TransparencyIndex,
		Storage:           storageOf(fi.Compressed),
		Blend:             fi.Blend,
		Unknown12:         fi.Unknown2,
		Unknown20:         fi.Unknown3,
	}
}

// readLayeredFrame reads a composite frame: PtrFrameData points at LayerCount
// uint32 offsets of layer frame headers. Each layer is kept in Layers and
// drawn, in order, into a canvas the size of the outer frame. Layers align by
// hotspot, so a layer's top-left lands at (outer.Origin - layer.Origin); the
// parts of a layer outside the canvas are clipped. Transparent layer pixels
// (by the layer's own rule) leave lower layers visible; uncovered canvas
// pixels hold the outer frame's TransparencyIndex.
func (d *decoder) readLayeredFrame(offset uint32, fi *FrameInfo) (*Frame, error) {
	table := make([]byte, 4*int(fi.LayerCount))
	if err := d.readAt(int64(fi.PtrFrameData), table); err != nil {
		return nil, fmt.Errorf("failed to read layer pointers at 0x%X: %w", fi.PtrFrameData, err)
	}
	size, err := frameByteCount(fi.Width, fi.Height)
	if err != nil {
		return nil, err
	}
	if err := d.charge(2 * size); err != nil {
		return nil, err
	}

	frame := newFrame(fi)
	w, h := int(fi.Width), int(fi.Height)
	canvas := make([]byte, size)
	for i := range canvas {
		canvas[i] = fi.TransparencyIndex
	}
	covered := make([]bool, size)

	for li := 0; li < int(fi.LayerCount); li++ {
		p := binary.LittleEndian.Uint32(table[4*li:])
		if p == offset {
			d.warn(int64(offset), "layer %d refers to its own frame header; the game cannot draw it, so it was skipped", li)
			continue
		}
		layer, err := d.readFrame(p, true)
		if err != nil {
			return nil, fmt.Errorf("failed to read layer %d at 0x%X: %w", li, p, err)
		}
		if layer == nil {
			continue
		}
		layer.Duration = 0
		frame.Layers = append(frame.Layers, layer)

		// Draw only the part of the layer inside the canvas, so the work is
		// bounded by pixels already charged to the budget.
		dx := int(fi.OriginX) - int(layer.OriginX)
		dy := int(fi.OriginY) - int(layer.OriginY)
		lw := int(layer.Width)
		x0, x1 := max(0, -dx), min(lw, w-dx)
		y0, y1 := max(0, -dy), min(int(layer.Height), h-dy)
		if x0 >= x1 {
			continue
		}
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				src := y*lw + x
				if !layer.PixelOpaque(src) {
					continue
				}
				dst := (dy+y)*w + dx + x
				canvas[dst] = layer.Pixels[src]
				covered[dst] = true
			}
		}
	}

	frame.Pixels = canvas
	for i, c := range covered {
		if c && canvas[i] == fi.TransparencyIndex {
			frame.Opaque = covered
			break
		}
	}
	return frame, nil
}

// readSimpleFrame reads pixel data for a simple (non-layered) frame. A frame
// with no pixels reads no data, whatever its data offset.
func (d *decoder) readSimpleFrame(offset uint32, fi *FrameInfo) (*Frame, error) {
	size, err := frameByteCount(fi.Width, fi.Height)
	if err != nil {
		return nil, err
	}
	frame := newFrame(fi)
	if size == 0 {
		frame.Pixels = []byte{}
		return frame, nil
	}
	if err := d.charge(size); err != nil {
		return nil, err
	}
	if _, err := d.file.Seek(int64(fi.PtrFrameData), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to pixel data at offset 0x%X: %w", fi.PtrFrameData, err)
	}
	if fi.Compressed != 0 {
		err = d.readCompressed(offset, frame)
	} else {
		frame.Pixels = make([]byte, size)
		if _, err = io.ReadFull(d.file, frame.Pixels); err != nil {
			err = fmt.Errorf("failed to read uncompressed pixels: %w", err)
		}
	}
	if err != nil {
		return nil, err
	}
	return frame, nil
}

// readCompressed decodes row-compressed pixel data. Each row is a uint16 byte
// count followed by commands:
//
//	bit 0 set:   skip (mask>>1) transparent pixels
//	bit 1 set:   repeat the next byte (mask>>2)+1 times
//	otherwise:   copy the next (mask>>2)+1 bytes
//
// Repeated and copied pixels are drawn even when they equal the frame's
// transparency index; only skipped pixels (and rows with a zero byte count)
// are transparent. When a frame draws a key-valued pixel, the reader records
// the frame's Opaque mask. A row whose commands end before the frame width is
// padded with transparent pixels and reported. Every row's size word and
// bytes are charged to the decode budget before they are read, since many
// frame headers may point at the same row data.
func (d *decoder) readCompressed(offset uint32, f *Frame) error {
	width := int(f.Width)
	key := f.TransparencyIndex
	pixels := make([]byte, int(f.Width)*int(f.Height))
	for i := range pixels {
		pixels[i] = key
	}
	var opaque []bool
	draw := func(at int, v byte) error {
		pixels[at] = v
		if opaque != nil {
			opaque[at] = true
			return nil
		}
		if v == key {
			if err := d.charge(len(pixels)); err != nil {
				return err
			}
			opaque = make([]bool, len(pixels))
			for i := 0; i < at; i++ {
				opaque[i] = pixels[i] != key
			}
			opaque[at] = true
		}
		return nil
	}

	shortRows, firstShort := 0, 0
	var sizeBuf [2]byte
	for row := 0; row < int(f.Height); row++ {
		if _, err := io.ReadFull(d.file, sizeBuf[:]); err != nil {
			return fmt.Errorf("failed to read row %d size: %w", row, err)
		}
		rowSize := int(binary.LittleEndian.Uint16(sizeBuf[:]))
		if err := d.charge(2 + rowSize); err != nil {
			return err
		}
		if cap(d.rowBuf) < rowSize {
			d.rowBuf = make([]byte, rowSize)
		}
		data := d.rowBuf[:rowSize]
		if _, err := io.ReadFull(d.file, data); err != nil {
			return fmt.Errorf("failed to read compressed row %d: %w", row, err)
		}

		base := row * width
		x, i := 0, 0
		for i < len(data) && x < width {
			mask := data[i]
			i++
			switch {
			case mask&0x01 != 0:
				x += min(int(mask>>1), width-x)
			case mask&0x02 != 0:
				if i >= len(data) {
					continue // repeat value missing: the row ends here
				}
				v := data[i]
				i++
				n := min(int(mask>>2)+1, width-x)
				for j := 0; j < n; j++ {
					if err := draw(base+x+j, v); err != nil {
						return err
					}
				}
				x += n
			default:
				n := min(int(mask>>2)+1, width-x)
				if n > len(data)-i {
					n = len(data) - i
				}
				for j := 0; j < n; j++ {
					if err := draw(base+x+j, data[i+j]); err != nil {
						return err
					}
				}
				i += n
				x += n
			}
		}
		if rowSize != 0 && x < width {
			if shortRows == 0 {
				firstShort = row
			}
			shortRows++
		}
	}
	if shortRows > 0 {
		d.warn(int64(offset), "%d compressed rows (first: row %d) end before the frame width; the rest of each is left transparent (the game would read on into the following bytes)", shortRows, firstShort)
	}
	f.Pixels = pixels
	f.Opaque = opaque
	return nil
}

// nullTerminatedString converts a null-terminated byte array to a string.
func nullTerminatedString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
