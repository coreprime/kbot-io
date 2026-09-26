package smacker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Smacker file header constants
const (
	SignatureSMK2 = 0x324B4D53 // "SMK2" as little-endian uint32
	SignatureSMK4 = 0x344B4D53 // "SMK4" as little-endian uint32

	// HeaderSize is the size of the fixed header. The frame-size table
	// starts at this offset, directly after the header's final (unused)
	// dword.
	HeaderSize = 104

	// AudioTrackCount is the number of audio tracks a header describes.
	AudioTrackCount = 7

	// DefaultFrameRate is the rate, in frames per second, that FrameRate
	// reports for a header whose frame-rate field is 0. That value has no
	// defined timing in the header: FFmpeg rejects it, other Smacker
	// decoders use 10 fps, and the rate TA 3.1c plays such a file at has not
	// been established. The shipped TA movies all store -3333 (about 30 fps).
	DefaultFrameRate = 15.0
)

// ErrTruncated reports a file too short for the sizes its header declares.
var ErrTruncated = errors.New("smacker: file is truncated")

// Header flag bits (Header.Flags).
const (
	// FlagRingFrame marks a movie whose tables carry one extra "ring"
	// frame after the last frame, which leads back to the first frame so the
	// movie can loop. The header's frame count does not include it.
	FlagRingFrame = 0x01
	// FlagInterlaced marks a movie the game shows at twice its stored
	// height with every second line left black: stored line n is drawn on
	// display line 2n and display line 2n+1 stays blank.
	FlagInterlaced = 0x02
	// FlagDoubled marks a movie the game shows at twice its stored height
	// by drawing every stored line twice.
	FlagDoubled = 0x04
)

// HeightMode says how a movie's stored lines map to the lines the game
// shows, from the FlagInterlaced and FlagDoubled bits.
type HeightMode int

const (
	// HeightNormal shows each stored line once.
	HeightNormal HeightMode = iota
	// HeightInterlaced (FlagInterlaced alone) shows each stored line
	// followed by a black line.
	HeightInterlaced
	// HeightDoubled (FlagDoubled alone) shows each stored line twice.
	HeightDoubled
)

// String returns "normal", "interlaced" or "doubled".
func (m HeightMode) String() string {
	switch m {
	case HeightInterlaced:
		return "interlaced"
	case HeightDoubled:
		return "doubled"
	default:
		return "normal"
	}
}

// Header represents a Smacker video file header
type Header struct {
	Signature uint32 // Should be "SMK2" or "SMK4"
	Width     uint32
	Height    uint32
	Frames    uint32
	// FrameRate encodes the frame time. A positive value n is n milliseconds
	// per frame (1000/n fps); a negative value n is -n hundred-thousandths of
	// a second per frame (100000/-n fps, so -3333 is about 30 fps); 0 has no
	// defined timing (see DefaultFrameRate).
	FrameRate int32
	Flags     uint32
	// AudioSize holds, per track, the size of the largest audio chunk in any
	// frame (a buffer size, not the track's total size).
	AudioSize [AudioTrackCount]uint32
	TreesSize uint32
	MMapSize  uint32
	MClrSize  uint32
	FullSize  uint32
	TypeSize  uint32
	// AudioRate holds each track's packed audio word as stored: the sample
	// rate in the low 24 bits and the track's format flags in the high byte.
	// Use AudioTrack to decode it.
	AudioRate [AudioTrackCount]uint32
	// AudioFlags holds the high byte of each AudioRate word (the track's
	// format flags). The file has no separate flags table.
	//
	// Deprecated: use AudioTrack, which decodes the packed word.
	AudioFlags [AudioTrackCount]uint32
	// FrameSizes and FrameTypes hold every frame-table entry as stored:
	// Frames entries, plus a last entry for the ring frame when RingFrame
	// is 1. Each size is used whole as the frame's payload size.
	FrameSizes   []uint32
	FrameTypes   []byte
	HuffmanTrees []byte // Huffman trees data
	// RingFrame is 1 when FlagRingFrame is set and the tables carry the
	// extra ring-frame entry, otherwise 0.
	RingFrame uint32
}

// Reader wraps a Smacker video file for reading
type Reader struct {
	file   *os.File
	header *Header
	size   int64
}

// OpenReader opens a Smacker video file for reading
func OpenReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	header, err := readHeader(f, st.Size())
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	return &Reader{
		file:   f,
		header: header,
		size:   st.Size(),
	}, nil
}

// NewReader parses the Smacker header and frame tables from r, which holds
// size bytes. The returned Reader does not own r; Close is a no-op for it.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	header, err := readHeader(r, size)
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}
	return &Reader{header: header, size: size}, nil
}

// Close closes the reader
func (r *Reader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// Header returns the video header
func (r *Reader) Header() *Header {
	return r.header
}

// Width returns video width
func (r *Reader) Width() int {
	return int(r.header.Width)
}

// Height returns video height
func (r *Reader) Height() int {
	return int(r.header.Height)
}

// DisplayHeight returns the number of lines the game shows; see
// Header.DisplayHeight.
func (r *Reader) DisplayHeight() int {
	return r.header.DisplayHeight()
}

// HeightMode returns how the stored lines are shown; see Header.HeightMode.
func (r *Reader) HeightMode() HeightMode {
	return r.header.HeightMode()
}

// HeightMode decodes the height bits of Flags. The game doubles the height
// only when exactly one of FlagInterlaced and FlagDoubled is set; a header
// with both is shown at its stored height.
func (h *Header) HeightMode() HeightMode {
	switch h.Flags & (FlagInterlaced | FlagDoubled) {
	case FlagInterlaced:
		return HeightInterlaced
	case FlagDoubled:
		return HeightDoubled
	default:
		return HeightNormal
	}
}

// DisplayHeight returns the number of lines the game shows: twice the
// stored Height for HeightInterlaced and HeightDoubled, otherwise Height.
// The shipped 640x240 TA movies are interlaced and show as 640x480.
func (h *Header) DisplayHeight() int {
	if h.HeightMode() != HeightNormal {
		return 2 * int(h.Height)
	}
	return int(h.Height)
}

// FrameCount returns the number of frames the movie shows, excluding any
// ring frame.
func (r *Reader) FrameCount() int {
	return int(r.header.Frames)
}

// HasRingFrame reports whether the tables carry an extra ring frame.
func (r *Reader) HasRingFrame() bool {
	return r.header.HasRingFrame()
}

// HasRingFrame reports whether the tables carry an extra ring frame
// (FlagRingFrame).
func (h *Header) HasRingFrame() bool {
	return h.RingFrame != 0
}

// FrameRate returns frames per second; see Header.FramesPerSecond.
func (r *Reader) FrameRate() float64 {
	return r.header.FramesPerSecond()
}

// Duration returns the video duration in seconds: FrameCount frames at
// FrameRate.
func (r *Reader) Duration() float64 {
	return float64(r.header.Frames) / r.FrameRate()
}

// FramesPerSecond decodes FrameRate. A positive value is milliseconds per
// frame, a negative value hundred-thousandths of a second per frame, and 0
// gives DefaultFrameRate. The result is always positive.
func (h *Header) FramesPerSecond() float64 {
	switch {
	case h.FrameRate > 0:
		return 1000.0 / float64(h.FrameRate)
	case h.FrameRate < 0:
		// Negate in float64: -math.MinInt32 does not fit in an int32.
		return 100000.0 / -float64(h.FrameRate)
	default:
		return DefaultFrameRate
	}
}

// SignatureString returns the four-character signature ("SMK2" or "SMK4").
func (r *Reader) SignatureString() string {
	s := r.header.Signature
	return string([]byte{byte(s), byte(s >> 8), byte(s >> 16), byte(s >> 24)})
}

// HasAudio reports whether any audio track is present, that is, whether any
// track's packed audio word has the present bit set.
func (r *Reader) HasAudio() bool {
	return r.header.HasAudio()
}

// AudioTrack decodes audio track i (0 to AudioTrackCount-1). Out-of-range
// indices return a zero AudioTrack carrying the index.
func (r *Reader) AudioTrack(i int) AudioTrack {
	return r.header.AudioTrack(i)
}

// AudioTracks returns the present audio tracks in index order.
func (r *Reader) AudioTracks() []AudioTrack {
	return r.header.AudioTracks()
}

// readHeader reads and parses the Smacker header, frame tables and Huffman
// trees from r, which holds size bytes.
func readHeader(r io.ReaderAt, size int64) (*Header, error) {
	if size < HeaderSize {
		return nil, fmt.Errorf("%w: %d bytes, shorter than the %d-byte header", ErrTruncated, size, HeaderSize)
	}
	buf := make([]byte, HeaderSize)
	if err := readAt(r, buf, 0); err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	h := &Header{Signature: le.Uint32(buf[0:])}

	// Verify signature
	if h.Signature != SignatureSMK2 && h.Signature != SignatureSMK4 {
		return nil, fmt.Errorf("invalid signature: 0x%08X (expected SMK2 or SMK4)", h.Signature)
	}

	h.Width = le.Uint32(buf[4:])
	h.Height = le.Uint32(buf[8:])
	h.Frames = le.Uint32(buf[12:])
	h.FrameRate = int32(le.Uint32(buf[16:]))
	h.Flags = le.Uint32(buf[20:])
	for i := 0; i < AudioTrackCount; i++ {
		h.AudioSize[i] = le.Uint32(buf[24+4*i:])
	}
	h.TreesSize = le.Uint32(buf[52:])
	h.MMapSize = le.Uint32(buf[56:])
	h.MClrSize = le.Uint32(buf[60:])
	h.FullSize = le.Uint32(buf[64:])
	h.TypeSize = le.Uint32(buf[68:])
	for i := 0; i < AudioTrackCount; i++ {
		h.AudioRate[i] = le.Uint32(buf[72+4*i:])
		h.AudioFlags[i] = h.AudioRate[i] >> 24
	}
	// Bytes 100-103 are an unused dword; the frame-size table follows.

	if h.Flags&FlagRingFrame != 0 {
		h.RingFrame = 1
	}
	entries := int64(h.Frames) + int64(h.RingFrame)
	off := int64(HeaderSize)

	// Check the tables and trees fit in the file before allocating
	// anything from the header's counts, so a corrupt count cannot ask for
	// gigabytes.
	need := uint64(HeaderSize) + 5*uint64(entries) + uint64(h.TreesSize)
	if need > uint64(size) || need > math.MaxInt {
		return nil, fmt.Errorf("%w: %d frame-table entries and %d bytes of Huffman trees need %d bytes, but the file is %d bytes",
			ErrTruncated, entries, h.TreesSize, need, size)
	}

	// Frame-size table: one little-endian dword per entry.
	table := make([]byte, 4*entries)
	if err := readAt(r, table, off); err != nil {
		return nil, fmt.Errorf("frame-size table: %w", err)
	}
	off += int64(len(table))
	h.FrameSizes = make([]uint32, entries)
	for i := range h.FrameSizes {
		h.FrameSizes[i] = le.Uint32(table[4*i:])
	}

	// Frame-type table: one byte per entry.
	h.FrameTypes = make([]byte, entries)
	if err := readAt(r, h.FrameTypes, off); err != nil {
		return nil, fmt.Errorf("frame-type table: %w", err)
	}
	off += entries

	// Read Huffman trees
	if h.TreesSize > 0 {
		h.HuffmanTrees = make([]byte, h.TreesSize)
		if err := readAt(r, h.HuffmanTrees, off); err != nil {
			return nil, fmt.Errorf("huffman trees: %w", err)
		}
	}

	return h, nil
}

// FrameDataOffset returns the file offset of the first frame's payload: the
// header, the frame-size and frame-type tables and the Huffman trees come
// before it.
func (h *Header) FrameDataOffset() int64 {
	return HeaderSize + 5*int64(len(h.FrameSizes)) + int64(h.TreesSize)
}

// FrameDataSize returns the total size of the frame payloads, summing every
// frame-size table entry. The game uses each whole entry as the payload size.
func (h *Header) FrameDataSize() uint64 {
	var total uint64
	for _, s := range h.FrameSizes {
		total += uint64(s)
	}
	return total
}

// readAt fills buf from r at off, reporting a short read as ErrTruncated.
func readAt(r io.ReaderAt, buf []byte, off int64) error {
	if len(buf) == 0 {
		return nil
	}
	n, err := r.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %w", ErrTruncated, io.ErrUnexpectedEOF)
	}
	return err
}

// Info returns a formatted string with video information
func (r *Reader) Info() string {
	info := "Smacker Video File\n"
	if r.Version() == 2 {
		info += fmt.Sprintf("  Signature: %s\n", r.SignatureString())
	} else {
		info += fmt.Sprintf("  Signature: %s (TA plays SMK2 only)\n", r.SignatureString())
	}
	info += fmt.Sprintf("  Resolution: %dx%d\n", r.Width(), r.Height())
	if mode := r.HeightMode(); mode != HeightNormal {
		info += fmt.Sprintf("  Display: %dx%d (%s)\n", r.Width(), r.DisplayHeight(), mode)
	}
	info += fmt.Sprintf("  Frames: %d\n", r.FrameCount())
	if r.HasRingFrame() {
		info += "  Ring Frame: yes\n"
	}
	info += fmt.Sprintf("  Frame Rate: %.2f fps\n", r.FrameRate())
	info += fmt.Sprintf("  Duration: %.2f seconds\n", r.Duration())
	info += fmt.Sprintf("  Has Audio: %v\n", r.HasAudio())

	if tracks := r.AudioTracks(); len(tracks) > 0 {
		info += "  Audio Tracks:\n"
		for _, t := range tracks {
			info += fmt.Sprintf("    Track %d: %s\n", t.Index, t)
		}
	}

	return info
}
