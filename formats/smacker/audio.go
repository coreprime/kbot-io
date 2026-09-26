package smacker

import (
	"fmt"
	"strings"
)

// Bits of a track's packed audio word (Header.AudioRate).
const (
	// AudioRateMask selects the sample rate in hertz.
	AudioRateMask = 0x00FFFFFF
	// AudioCompressed marks a track whose samples are compressed.
	AudioCompressed = 0x80000000
	// AudioPresent marks a track that carries audio data. The game uses a
	// track only when this bit is set.
	AudioPresent = 0x40000000
	// Audio16Bit marks 16-bit samples; when clear the samples are 8-bit.
	Audio16Bit = 0x20000000
	// AudioStereo marks two-channel audio; when clear the track is mono.
	AudioStereo = 0x10000000
)

// AudioTrack is one audio track decoded from a Smacker header.
type AudioTrack struct {
	// Index is the track number, 0 to AudioTrackCount-1.
	Index int
	// Present reports whether the track carries audio (AudioPresent). A
	// track that is not present is ignored by the game even when its rate
	// is non-zero.
	Present bool
	// SampleRate is the sample rate in hertz (the low 24 bits of the word).
	SampleRate uint32
	// Flags is the high byte of the packed word.
	Flags uint8
	// Compressed, SixteenBit and Stereo decode the individual flag bits.
	Compressed bool
	SixteenBit bool
	Stereo     bool
	// MaxChunkSize is the size in bytes of the track's largest per-frame
	// audio chunk (Header.AudioSize).
	MaxChunkSize uint32
}

// Channels returns 2 for a stereo track and 1 otherwise.
func (t AudioTrack) Channels() int {
	if t.Stereo {
		return 2
	}
	return 1
}

// BitsPerSample returns 16 or 8.
func (t AudioTrack) BitsPerSample() int {
	if t.SixteenBit {
		return 16
	}
	return 8
}

// String describes the track, for example "22050 Hz, 2 channels, 8-bit,
// compressed".
func (t AudioTrack) String() string {
	parts := []string{
		fmt.Sprintf("%d Hz", t.SampleRate),
		fmt.Sprintf("%d channels", t.Channels()),
		fmt.Sprintf("%d-bit", t.BitsPerSample()),
	}
	if t.Compressed {
		parts = append(parts, "compressed")
	}
	if !t.Present {
		parts = append(parts, "not present")
	}
	return strings.Join(parts, ", ")
}

// AudioTrack decodes audio track i (0 to AudioTrackCount-1) from its packed
// audio word. Out-of-range indices return a zero AudioTrack carrying the
// index.
func (h *Header) AudioTrack(i int) AudioTrack {
	t := AudioTrack{Index: i}
	if i < 0 || i >= AudioTrackCount {
		return t
	}
	w := h.AudioRate[i]
	t.Present = w&AudioPresent != 0
	t.SampleRate = w & AudioRateMask
	t.Flags = uint8(w >> 24)
	t.Compressed = w&AudioCompressed != 0
	t.SixteenBit = w&Audio16Bit != 0
	t.Stereo = w&AudioStereo != 0
	t.MaxChunkSize = h.AudioSize[i]
	return t
}

// AudioTracks returns the present audio tracks in index order.
func (h *Header) AudioTracks() []AudioTrack {
	var out []AudioTrack
	for i := 0; i < AudioTrackCount; i++ {
		if t := h.AudioTrack(i); t.Present {
			out = append(out, t)
		}
	}
	return out
}

// HasAudio reports whether any track has the present bit set.
func (h *Header) HasAudio() bool {
	for i := 0; i < AudioTrackCount; i++ {
		if h.AudioRate[i]&AudioPresent != 0 {
			return true
		}
	}
	return false
}
