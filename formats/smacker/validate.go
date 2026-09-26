package smacker

import (
	"errors"
	"fmt"
)

// Errors wrapped by Validate, for use with errors.Is.
var (
	// ErrUnsupportedVersion reports a file other than SMK2. TA 3.1c plays
	// SMK2 movies only; SMK4 files from newer tools parse here but do not
	// play in the game.
	ErrUnsupportedVersion = errors.New("smacker: TA plays SMK2 files only")
	// ErrOutOfBounds reports a header value that is zero where it must not
	// be, or larger than the Limits allow.
	ErrOutOfBounds = errors.New("smacker: value out of bounds")
)

// Limits bounds the structural checks Validate applies. A zero field
// disables that check.
type Limits struct {
	MaxFileBytes  int64  // largest file size
	MaxWidth      uint32 // largest frame width in pixels
	MaxHeight     uint32 // largest stored frame height in pixels
	MaxFrames     uint32 // largest header frame count
	MaxTreeBytes  uint32 // largest Huffman tree block
	MaxFrameBytes uint32 // largest single frame payload
}

// DefaultLimits returns conservative bounds for movies meant to be played:
// files up to 512 MiB, frames up to 4096x4096, at most 100,000 frames,
// Huffman trees up to 64 MiB and frame payloads up to 16 MiB each. The
// shipped TA movies are far inside them.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:  512 << 20,
		MaxWidth:      4096,
		MaxHeight:     4096,
		MaxFrames:     100000,
		MaxTreeBytes:  64 << 20,
		MaxFrameBytes: 16 << 20,
	}
}

// Version returns the format version from the signature: 2 for SMK2 and 4
// for SMK4.
func (h *Header) Version() int {
	return int(byte(h.Signature>>24) - '0')
}

// Version returns the format version from the signature: 2 for SMK2 and 4
// for SMK4. TA 3.1c plays version 2 only.
func (r *Reader) Version() int {
	return r.header.Version()
}

// Validate checks the movie against limits and the file size, beyond what
// opening it requires: the signature must be SMK2, the geometry and frame
// count must be non-zero and within limits, the Huffman trees and every
// frame payload must be within limits, and the payloads must end within the
// file. It returns nil when every check passes, otherwise one error per
// problem joined with errors.Join; each wraps ErrUnsupportedVersion,
// ErrOutOfBounds or ErrTruncated. Frame data is not decoded.
func (r *Reader) Validate(limits Limits) error {
	h := r.header
	var errs []error
	fail := func(kind error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{kind}, args...)...))
	}

	if h.Signature != SignatureSMK2 {
		fail(ErrUnsupportedVersion, "signature is %s", r.SignatureString())
	}
	if limits.MaxFileBytes > 0 && r.size > limits.MaxFileBytes {
		fail(ErrOutOfBounds, "file is %d bytes, limit %d", r.size, limits.MaxFileBytes)
	}
	if h.Width == 0 || h.Height == 0 {
		fail(ErrOutOfBounds, "frame size is %dx%d", h.Width, h.Height)
	}
	if limits.MaxWidth > 0 && h.Width > limits.MaxWidth {
		fail(ErrOutOfBounds, "width %d exceeds %d", h.Width, limits.MaxWidth)
	}
	if limits.MaxHeight > 0 && h.Height > limits.MaxHeight {
		fail(ErrOutOfBounds, "height %d exceeds %d", h.Height, limits.MaxHeight)
	}
	if h.Frames == 0 {
		fail(ErrOutOfBounds, "frame count is 0")
	}
	if limits.MaxFrames > 0 && h.Frames > limits.MaxFrames {
		fail(ErrOutOfBounds, "frame count %d exceeds %d", h.Frames, limits.MaxFrames)
	}
	if limits.MaxTreeBytes > 0 && h.TreesSize > limits.MaxTreeBytes {
		fail(ErrOutOfBounds, "Huffman trees are %d bytes, limit %d", h.TreesSize, limits.MaxTreeBytes)
	}
	if limits.MaxFrameBytes > 0 {
		over, first := 0, -1
		for i, s := range h.FrameSizes {
			if s > limits.MaxFrameBytes {
				if first < 0 {
					first = i
				}
				over++
			}
		}
		if over > 0 {
			fail(ErrOutOfBounds, "%d frame payloads exceed %d bytes (first: entry %d, %d bytes)",
				over, limits.MaxFrameBytes, first, h.FrameSizes[first])
		}
	}
	if end := uint64(h.FrameDataOffset()) + h.FrameDataSize(); end > uint64(r.size) {
		fail(ErrTruncated, "frame payloads end at byte %d, but the file is %d bytes", end, r.size)
	}
	return errors.Join(errs...)
}
