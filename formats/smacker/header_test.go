package smacker_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/smacker"
)

// TestTablesStartAfterHeader pins the table layout: the frame-size table
// starts at byte 104, straight after the fixed header, followed by the
// frame-type table, the Huffman trees and then the frame payloads.
func TestTablesStartAfterHeader(t *testing.T) {
	s := defaultSpec()
	data := s.bytes()
	r := s.open(t)
	h := r.Header()

	if got, want := h.FrameSizes, s.frameSizes; !reflect.DeepEqual(got, want) {
		t.Errorf("FrameSizes = %v, want %v", got, want)
	}
	if got, want := h.FrameTypes, s.frameTypes; !bytes.Equal(got, want) {
		t.Errorf("FrameTypes = %x, want %x", got, want)
	}
	if got, want := h.HuffmanTrees, s.trees; !bytes.Equal(got, want) {
		t.Errorf("HuffmanTrees = %x, want %x", got, want)
	}
	if h.TreesSize != uint32(len(s.trees)) || h.MMapSize != 0x1111 || h.MClrSize != 0x2222 ||
		h.FullSize != 0x3333 || h.TypeSize != 0x4444 {
		t.Errorf("tree sizes = %#x %#x %#x %#x %#x", h.TreesSize, h.MMapSize, h.MClrSize, h.FullSize, h.TypeSize)
	}

	wantOffset := int64(smacker.HeaderSize + 5*3 + len(s.trees))
	if got := h.FrameDataOffset(); got != wantOffset {
		t.Errorf("FrameDataOffset = %d, want %d", got, wantOffset)
	}
	if got := h.FrameDataSize(); got != 40 {
		t.Errorf("FrameDataSize = %d, want 40", got)
	}
	if end := h.FrameDataOffset() + int64(h.FrameDataSize()); end != int64(len(data)) {
		t.Errorf("payloads end at %d, file is %d bytes", end, len(data))
	}
	// The first payload byte is the filler of frame 0.
	if data[h.FrameDataOffset()] != 0xF0 {
		t.Errorf("byte at FrameDataOffset = %#x, want frame 0 filler 0xF0", data[h.FrameDataOffset()])
	}
}

// TestSmallFileOpens checks that a minimal one-frame movie opens: the
// reader must not expect any data between the header and the tables.
func TestSmallFileOpens(t *testing.T) {
	s := smkSpec{sig: "SMK2", width: 8, height: 8, frames: 1, rate: 100,
		frameSizes: []uint32{5}, trees: make([]byte, 10)}
	data := s.bytes()
	if len(data) != 124 {
		t.Fatalf("fixture is %d bytes, want 124", len(data))
	}
	r := s.open(t)
	if r.FrameCount() != 1 || r.Header().FrameSizes[0] != 5 {
		t.Errorf("frames = %d, sizes = %v", r.FrameCount(), r.Header().FrameSizes)
	}
}

// TestAudioWordDecoding checks the packed audio word: sample rate in the low
// 24 bits, format flags in the high byte, and presence from bit 0x40000000.
func TestAudioWordDecoding(t *testing.T) {
	s := defaultSpec()
	s.audioRate[1] = 0x00005622 // a rate without the present bit
	s.audioRate[2] = 0x60003E80 // present, 16-bit, mono, uncompressed, 16000 Hz
	s.audioSize[2] = 777
	r := s.open(t)
	h := r.Header()

	t0 := r.AudioTrack(0)
	want0 := smacker.AudioTrack{Index: 0, Present: true, SampleRate: 22050, Flags: 0xD0,
		Compressed: true, Stereo: true, MaxChunkSize: 45572}
	if t0 != want0 {
		t.Errorf("track 0 = %+v, want %+v", t0, want0)
	}
	if t0.Channels() != 2 || t0.BitsPerSample() != 8 {
		t.Errorf("track 0 channels/bits = %d/%d, want 2/8", t0.Channels(), t0.BitsPerSample())
	}
	if t1 := r.AudioTrack(1); t1.Present || t1.SampleRate != 22050 {
		t.Errorf("track 1 = %+v, want not present at 22050 Hz", t1)
	}
	t2 := r.AudioTrack(2)
	if !t2.Present || t2.SampleRate != 16000 || !t2.SixteenBit || t2.Stereo || t2.Compressed ||
		t2.Channels() != 1 || t2.BitsPerSample() != 16 || t2.MaxChunkSize != 777 {
		t.Errorf("track 2 = %+v", t2)
	}
	if got := r.AudioTrack(7); got != (smacker.AudioTrack{Index: 7}) {
		t.Errorf("out-of-range track = %+v", got)
	}

	var idx []int
	for _, tr := range r.AudioTracks() {
		idx = append(idx, tr.Index)
	}
	if !reflect.DeepEqual(idx, []int{0, 2}) {
		t.Errorf("AudioTracks indices = %v, want [0 2]", idx)
	}
	if !r.HasAudio() {
		t.Error("HasAudio = false, want true")
	}
	// The raw word stays available; AudioFlags mirrors its high byte.
	if h.AudioRate[0] != 0xD0005622 || h.AudioFlags[0] != 0xD0 || h.AudioFlags[1] != 0 {
		t.Errorf("AudioRate[0] = %#x, AudioFlags = %#x", h.AudioRate[0], h.AudioFlags)
	}

	info := r.Info()
	if !strings.Contains(info, "Track 0: 22050 Hz, 2 channels, 8-bit, compressed") {
		t.Errorf("Info() lacks the decoded track 0:\n%s", info)
	}
	if strings.Contains(info, "Track 1") || !strings.Contains(info, "Track 2: 16000 Hz, 1 channels, 16-bit") {
		t.Errorf("Info() should list tracks 0 and 2 only:\n%s", info)
	}
}

// TestNoAudioWithoutPresentBit checks that a rate alone does not make a
// track present.
func TestNoAudioWithoutPresentBit(t *testing.T) {
	s := defaultSpec()
	s.audioRate[0] = 0x90005622 // compressed and stereo, but not present
	r := s.open(t)
	if r.HasAudio() || len(r.AudioTracks()) != 0 {
		t.Errorf("HasAudio = %v, tracks = %v; want no audio", r.HasAudio(), r.AudioTracks())
	}
	if strings.Contains(r.Info(), "Audio Tracks") {
		t.Errorf("Info() lists audio tracks:\n%s", r.Info())
	}
}

// TestFrameRate checks the frame-rate encodings: positive values are
// milliseconds per frame, negative values hundred-thousandths of a second.
func TestFrameRate(t *testing.T) {
	cases := []struct {
		rate     int32
		fps      float64
		duration float64 // for 3 frames
	}{
		{rate: 50, fps: 20, duration: 0.15},
		{rate: 1000, fps: 1, duration: 3},
		{rate: -3333, fps: 100000.0 / 3333, duration: 3 * 3333 / 100000.0},
		{rate: -10000, fps: 10, duration: 0.3},
		{rate: 0, fps: smacker.DefaultFrameRate, duration: 3 / smacker.DefaultFrameRate},
		// The most negative value must not overflow into a negative rate.
		{rate: -2147483648, fps: 100000.0 / 2147483648, duration: 3 * 2147483648 / 100000.0},
	}
	for _, c := range cases {
		s := defaultSpec()
		s.rate = c.rate
		r := s.open(t)
		if got := r.FrameRate(); !near(got, c.fps) {
			t.Errorf("rate %d: FrameRate = %v, want %v", c.rate, got, c.fps)
		}
		if got := r.Header().FramesPerSecond(); !near(got, c.fps) {
			t.Errorf("rate %d: FramesPerSecond = %v, want %v", c.rate, got, c.fps)
		}
		if got := r.Duration(); !near(got, c.duration) {
			t.Errorf("rate %d: Duration = %v, want %v", c.rate, got, c.duration)
		}
	}
}

func near(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1e-9*(1+b)
}

// TestRingFrame checks that the ring-frame flag adds one entry to both
// tables, moving the Huffman trees and payloads 5 bytes later, while the
// frame count stays at the header's value.
func TestRingFrame(t *testing.T) {
	s := defaultSpec()
	s.flags = smacker.FlagRingFrame
	s.frameSizes = []uint32{12, 20, 8, 16} // three frames plus the ring frame
	s.frameTypes = []byte{0x81, 0x02, 0x04, 0x08}
	data := s.bytes()
	r := s.open(t)
	h := r.Header()

	if !r.HasRingFrame() || h.RingFrame != 1 {
		t.Errorf("HasRingFrame = %v, RingFrame = %d; want true, 1", r.HasRingFrame(), h.RingFrame)
	}
	if r.FrameCount() != 3 {
		t.Errorf("FrameCount = %d, want 3 (ring frame excluded)", r.FrameCount())
	}
	if !reflect.DeepEqual(h.FrameSizes, s.frameSizes) || !bytes.Equal(h.FrameTypes, s.frameTypes) {
		t.Errorf("tables = %v / %x, want %v / %x", h.FrameSizes, h.FrameTypes, s.frameSizes, s.frameTypes)
	}
	if !bytes.Equal(h.HuffmanTrees, s.trees) {
		t.Errorf("HuffmanTrees = %x, want %x", h.HuffmanTrees, s.trees)
	}
	if got, want := h.FrameDataOffset(), int64(smacker.HeaderSize+5*4+len(s.trees)); got != want {
		t.Errorf("FrameDataOffset = %d, want %d", got, want)
	}
	if end := h.FrameDataOffset() + int64(h.FrameDataSize()); end != int64(len(data)) {
		t.Errorf("payloads end at %d, file is %d bytes", end, len(data))
	}
	if !strings.Contains(r.Info(), "Ring Frame: yes") {
		t.Errorf("Info() does not mention the ring frame:\n%s", r.Info())
	}

	// Without the flag the same header must not read a fourth entry.
	plain := defaultSpec()
	if pr := plain.open(t); pr.HasRingFrame() || len(pr.Header().FrameSizes) != 3 {
		t.Errorf("unflagged file: ring = %v, %d entries", pr.HasRingFrame(), len(pr.Header().FrameSizes))
	}
}

// TestBoundsBeforeAllocating checks that header counts larger than the file
// are refused before any table is allocated from them.
func TestBoundsBeforeAllocating(t *testing.T) {
	cases := map[string]func(*smkSpec){
		"frame count 0xFFFFFFFF": func(s *smkSpec) { s.frames = 0xFFFFFFFF },
		"frame count 0xFFFFFFFF with ring frame": func(s *smkSpec) {
			s.frames = 0xFFFFFFFF
			s.flags = smacker.FlagRingFrame
		},
		"frame count one past the table": func(s *smkSpec) {
			s.frames = 4
			s.noPayload = true
			s.trees = nil
		},
		"tree size past the end": func(s *smkSpec) {
			s.noPayload = true
			s.trees = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := defaultSpec()
			mutate(&s)
			data := s.bytes()
			if name == "tree size past the end" {
				binary.LittleEndian.PutUint32(data[52:], 0xFFFFFFF0)
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, err := smacker.NewReader(bytes.NewReader(data), int64(len(data)))
			runtime.ReadMemStats(&after)
			if !errors.Is(err, smacker.ErrTruncated) {
				t.Fatalf("err = %v, want ErrTruncated", err)
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
				t.Errorf("allocated %d bytes before refusing the file", grew)
			}
		})
	}

	short := defaultSpec().bytes()[:smacker.HeaderSize-1]
	if _, err := smacker.NewReader(bytes.NewReader(short), int64(len(short))); !errors.Is(err, smacker.ErrTruncated) {
		t.Errorf("short header: err = %v, want ErrTruncated", err)
	}
}

// TestDisplayHeight checks the height modes: flag 2 (interlaced) and flag 4
// (doubled) each double the shown height; both together, or neither, keep
// the stored height.
func TestDisplayHeight(t *testing.T) {
	cases := []struct {
		flags  uint32
		mode   smacker.HeightMode
		height int
	}{
		{0, smacker.HeightNormal, 240},
		{smacker.FlagInterlaced, smacker.HeightInterlaced, 480},
		{smacker.FlagDoubled, smacker.HeightDoubled, 480},
		{smacker.FlagInterlaced | smacker.FlagDoubled, smacker.HeightNormal, 240},
		{smacker.FlagRingFrame | smacker.FlagInterlaced, smacker.HeightInterlaced, 480},
	}
	for _, c := range cases {
		s := defaultSpec()
		s.width, s.height = 640, 240
		s.flags = c.flags
		if c.flags&smacker.FlagRingFrame != 0 {
			s.frameSizes = append(s.frameSizes, 4)
			s.frameTypes = append(s.frameTypes, 0)
		}
		r := s.open(t)
		if got := r.HeightMode(); got != c.mode {
			t.Errorf("flags %#x: HeightMode = %v, want %v", c.flags, got, c.mode)
		}
		if got := r.DisplayHeight(); got != c.height {
			t.Errorf("flags %#x: DisplayHeight = %d, want %d", c.flags, got, c.height)
		}
		if r.Height() != 240 {
			t.Errorf("flags %#x: Height = %d, want the stored 240", c.flags, r.Height())
		}
		info := r.Info()
		if c.mode == smacker.HeightNormal && strings.Contains(info, "Display:") {
			t.Errorf("flags %#x: Info() has a Display line:\n%s", c.flags, info)
		}
		if c.mode != smacker.HeightNormal && !strings.Contains(info, "Display: 640x480 ("+c.mode.String()+")") {
			t.Errorf("flags %#x: Info() lacks the display size:\n%s", c.flags, info)
		}
	}
}
