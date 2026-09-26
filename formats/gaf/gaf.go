package gaf

import (
	"fmt"
	"strings"
)

// VersionTA is the header version word Cavedog's tools write. The game does
// not check the version word, and a few stock files (anims/terrain.gaf,
// anims/vismasks.gaf) store zero instead.
const VersionTA = 0x00010100

// Limits of the format as the game reads it.
const (
	// MaxSequences is the largest number of sequences a file can hold. The
	// game reads the low 16 bits of the header's sequence count as a signed
	// value, so a larger count, zero or a negative count means no sequences.
	MaxSequences = 32767
	// MaxFramesPerSequence is the largest frame count of a sequence (a
	// 16-bit field).
	MaxFramesPerSequence = 65535
	// MaxLayers is the largest layer count of a composite frame (frame byte
	// +10).
	MaxLayers = 255
	// MaxDuration is the largest frame duration in ticks. The game reads the
	// low 16 bits of the frame-list duration word.
	MaxDuration = 65535
	// MaxNameLength is the size in bytes of the sequence name field.
	MaxNameLength = 32
	// TicksPerSecond is the rate of the game's animation clock, the unit of
	// Frame.Duration.
	TicksPerSecond = 30
)

// isKnownGAFVersion reports whether a header version word is one of the two
// values stock files use. Other values are accepted with a warning.
func isKnownGAFVersion(v uint32) bool {
	return v == VersionTA || v == 0
}

// Header represents the GAF file header (12 bytes).
type Header struct {
	// Version is the version word, VersionTA or 0 in stock files. The game
	// ignores it.
	Version uint32
	// SequenceCount is the raw sequence count word. The game uses only its
	// signed low 16 bits; see EffectiveSequenceCount.
	SequenceCount uint32
	// Unknown1 is not interpreted by the game; stock files store 0.
	Unknown1 uint32
}

// EffectiveSequenceCount returns the number of sequences the game walks: the
// low 16 bits of SequenceCount read as a signed value, with zero or a
// negative value meaning none. The high 16 bits are ignored.
func (h Header) EffectiveSequenceCount() int {
	n := int(int16(uint16(h.SequenceCount)))
	if n < 0 {
		return 0
	}
	return n
}

// SequenceHeader represents an animation sequence header (40 bytes).
type SequenceHeader struct {
	FrameCount uint16 // Number of frames
	// Unknown1 is the loop word at +2 (Sequence.LoopFlags): the game loops
	// the sequence when its low byte is non-zero.
	Unknown1 uint16
	// Unknown2 is the word at +4 (Sequence.Unknown4); the game does not
	// interpret it.
	Unknown2 uint32
	Name     [32]byte // Sequence name, NUL-padded
}

// FrameListItem describes a frame entry (8 bytes).
type FrameListItem struct {
	PtrFrameInfo uint32 // Offset of the frame header
	// Duration is the display time in game ticks (1/30 s). The game reads
	// only the low 16 bits.
	Duration uint32
}

// FrameInfo is the on-disk frame header (24 bytes).
type FrameInfo struct {
	Width             uint16 // Frame width
	Height            uint16 // Frame height
	OriginX           int16  // X offset of the hotspot from the left edge
	OriginY           int16  // Y offset of the hotspot from the top edge
	TransparencyIndex uint8  // Transparent colour index (key)
	Compressed        uint8  // 0 = raw pixels, non-zero = row-compressed
	LayerCount        uint8  // Byte +10: number of layers (0 for a simple frame)
	Blend             uint8  // Byte +11: non-zero draws this frame, as a layer, translucently
	Unknown2          uint32 // Word at +12 (Frame.Unknown12)
	PtrFrameData      uint32 // Offset of the pixel data, or of the layer pointer table
	Unknown3          uint32 // Word at +20 (Frame.Unknown20)
}

// Sequence represents a complete animation sequence.
type Sequence struct {
	Name   string
	Frames []*Frame
	// LoopFlags is the sequence header word at +2. The game loops the
	// sequence when its low byte is non-zero and plays it once otherwise;
	// every stock sequence stores 1. See Loops and SetLoops.
	LoopFlags uint16
	// Unknown4 is the sequence header word at +4, kept so a rewritten file
	// keeps it. The game does not interpret it; stock files store 0.
	Unknown4 uint32
}

// Loops reports whether the game loops the sequence: the low byte of
// LoopFlags is non-zero.
func (s *Sequence) Loops() bool {
	return s != nil && s.LoopFlags&0xFF != 0
}

// SetLoops sets the low byte of LoopFlags to 1 (loop) or 0 (play once) and
// keeps the high byte.
func (s *Sequence) SetLoops(loop bool) {
	s.LoopFlags &^= 0xFF
	if loop {
		s.LoopFlags |= 1
	}
}

// FrameStorage says how a simple frame's pixels are stored.
type FrameStorage uint8

const (
	// StorageDefault leaves the choice to the writer (see
	// WriteOptions.DefaultStorage). Frames built in memory start with it.
	StorageDefault FrameStorage = iota
	// StorageRaw stores Width*Height palette indices. The game reads some
	// archives, such as unit textures and vismasks, as plain pixel arrays
	// and needs raw frames there (see StorageForPath).
	StorageRaw
	// StorageCompressed stores each row as a byte count followed by skip,
	// repeat and literal commands.
	StorageCompressed
)

// String returns "default", "raw" or "compressed".
func (s FrameStorage) String() string {
	switch s {
	case StorageRaw:
		return "raw"
	case StorageCompressed:
		return "compressed"
	case StorageDefault:
		return "default"
	default:
		return fmt.Sprintf("FrameStorage(%d)", uint8(s))
	}
}

// ParseFrameStorage parses "raw", "compressed" or "default" (an empty string
// also means default), ignoring case and surrounding space.
func ParseFrameStorage(s string) (FrameStorage, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return StorageDefault, nil
	case "raw":
		return StorageRaw, nil
	case "compressed":
		return StorageCompressed, nil
	default:
		return StorageDefault, fmt.Errorf("unknown GAF frame storage %q (want raw, compressed or default)", s)
	}
}

// StorageForPath returns the storage the game expects for the frames of the
// GAF at path (a VFS path; separators and case do not matter). The game reads
// unit textures (textures/*.gaf) and the sight masks (anims/vismasks.gaf) as
// plain pixel arrays, so their frames must be raw; for those it returns
// StorageRaw. For any other path it returns StorageDefault. Every stock
// texture frame is raw.
func StorageForPath(path string) FrameStorage {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	if strings.HasPrefix(p, "textures/") || strings.Contains(p, "/textures/") {
		return StorageRaw
	}
	if strings.HasPrefix(base, "vismask") {
		return StorageRaw
	}
	return StorageDefault
}

// Frame represents a single animation frame.
//
// A simple frame holds Width*Height palette indices in Pixels. A composite
// frame (one with Layers) is drawn by the game as its layers, each placed so
// that its hotspot lands on the frame's hotspot; the reader also flattens the
// layers into Pixels, clipped to the frame's own rectangle, so that every
// frame can be rendered the same way.
type Frame struct {
	Width             uint16
	Height            uint16
	OriginX           int16
	OriginY           int16
	TransparencyIndex uint8
	// Duration is the display time in game ticks (1/30 s), at most
	// MaxDuration. The game shows a frame for max(Duration, 1) ticks.
	Duration uint32
	Pixels   []byte // Palette indices, row by row

	// Storage is how the frame's pixels were stored (set by the reader) or
	// should be stored (read by the writer).
	Storage FrameStorage
	// Blend is frame byte +11. When it is non-zero on a layer, the game
	// draws that layer through its translucency table instead of copying
	// its pixels. The game ignores it on a frame drawn directly. No stock
	// file sets it. Renderers in this package draw such layers normally.
	Blend uint8
	// Opaque, when non-nil, has one entry per pixel and says which pixels
	// the game draws. The reader sets it only where the key test below
	// would be wrong: a compressed or composite frame that draws a pixel
	// whose value equals TransparencyIndex. When it is nil, a pixel is
	// transparent exactly when it equals TransparencyIndex. See PixelOpaque.
	Opaque []bool
	// Layers holds the layers of a composite frame, in drawing order. The
	// writer writes a frame with Layers as a composite (unless
	// WriteOptions.FlattenLayers is set) and ignores its Pixels. Layers are
	// always simple frames.
	Layers []*Frame
	// Unknown12 and Unknown20 are the frame header words at +12 and +20,
	// kept so a rewritten file keeps them. The game does not use them to
	// draw the frame.
	Unknown12 uint32
	Unknown20 uint32
}

// DisplayTicks returns how many ticks of the 30 Hz animation clock the game
// shows the frame for: the low 16 bits of Duration, and at least 1.
func (f *Frame) DisplayTicks() int {
	return max(int(f.Duration&0xFFFF), 1)
}

// TotalTicks returns the ticks one pass through the sequence takes: the sum
// of DisplayTicks over its frames.
func (s *Sequence) TotalTicks() int {
	total := 0
	for _, f := range s.Frames {
		if f != nil {
			total += f.DisplayTicks()
		}
	}
	return total
}

// PixelOpaque reports whether the game draws pixel i (an index into Pixels):
// the Opaque mask when it is set, otherwise whether the pixel differs from
// TransparencyIndex. Raw frames always use the key test; for compressed
// frames only skip commands are transparent. An index outside Pixels is not
// opaque.
func (f *Frame) PixelOpaque(i int) bool {
	if f == nil || i < 0 || i >= len(f.Pixels) {
		return false
	}
	if f.Opaque != nil {
		return i < len(f.Opaque) && f.Opaque[i]
	}
	return f.Pixels[i] != f.TransparencyIndex
}
