package smacker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
)

// Header represents a Smacker video file header
type Header struct {
	Signature uint32 // Should be "SMK2" or "SMK4"
	Width     uint32
	Height    uint32
	Frames    uint32
	FrameRate int32 // Microseconds per frame (negative = frames per second)
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
	AudioFlags   [AudioTrackCount]uint32
	FrameSizes   []uint32 // Array of frame sizes
	FrameTypes   []byte   // Array of frame types
	HuffmanTrees []byte   // Huffman trees data
	RingFrame    uint32
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

// FrameCount returns total number of frames
func (r *Reader) FrameCount() int {
	return int(r.header.Frames)
}

// FrameRate returns frames per second
func (r *Reader) FrameRate() float64 {
	if r.header.FrameRate < 0 {
		// Negative values appear to be stored as: -(100000 / fps)
		// For 30 fps: -(100000/30) = -3333.33 ≈ -3333
		// So to get fps: 100000 / abs(value)
		fps := 100000.0 / float64(-r.header.FrameRate)
		return fps
	}
	if r.header.FrameRate == 0 {
		return 15.0 // Default fallback
	}

	// Positive means microseconds per frame
	return 1000000.0 / float64(r.header.FrameRate)
}

// Duration returns video duration in seconds
func (r *Reader) Duration() float64 {
	return float64(r.header.Frames) / r.FrameRate()
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
		return nil, fmt.Errorf("file is %d bytes, shorter than the %d-byte header", size, HeaderSize)
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

	entries := int64(h.Frames)
	off := int64(HeaderSize)

	// Frame-size table: one little-endian dword per frame.
	table := make([]byte, 4*entries)
	if err := readAt(r, table, off); err != nil {
		return nil, fmt.Errorf("frame-size table: %w", err)
	}
	off += int64(len(table))
	h.FrameSizes = make([]uint32, entries)
	for i := range h.FrameSizes {
		h.FrameSizes[i] = le.Uint32(table[4*i:])
	}

	// Frame-type table: one byte per frame.
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

// readAt fills buf from r at off, treating a short read as io.ErrUnexpectedEOF.
func readAt(r io.ReaderAt, buf []byte, off int64) error {
	if len(buf) == 0 {
		return nil
	}
	n, err := r.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// Info returns a formatted string with video information
func (r *Reader) Info() string {
	info := "Smacker Video File\n"
	info += fmt.Sprintf("  Signature: %s\n", r.SignatureString())
	info += fmt.Sprintf("  Resolution: %dx%d\n", r.Width(), r.Height())
	info += fmt.Sprintf("  Frames: %d\n", r.FrameCount())
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
