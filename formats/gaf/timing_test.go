package gaf

import (
	"bytes"
	"encoding/binary"
	"image/gif"
	"testing"
)

func timedSequence(loop bool, durations ...uint32) *Sequence {
	s := &Sequence{Name: "t"}
	s.SetLoops(loop)
	for i, d := range durations {
		s.Frames = append(s.Frames, &Frame{Width: 2, Height: 1, TransparencyIndex: 9, Duration: d, Pixels: []byte{byte(i + 1), 9}})
	}
	return s
}

// apngChunks returns the payloads of every chunk of the given type.
func apngChunks(t *testing.T, b []byte, typ string) [][]byte {
	t.Helper()
	var out [][]byte
	for pos := 8; pos+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[pos:]))
		if string(b[pos+4:pos+8]) == typ {
			out = append(out, b[pos+8:pos+8+n])
		}
		pos += 12 + n
	}
	return out
}

func TestFrameDisplayTicks(t *testing.T) {
	for d, want := range map[uint32]int{0: 1, 1: 1, 2: 2, 65535: 65535, 0x00010003: 3} {
		if got := (&Frame{Duration: d}).DisplayTicks(); got != want {
			t.Errorf("DisplayTicks(%#x) = %d, want %d", d, got, want)
		}
	}
	if got := timedSequence(true, 0, 2, 5).TotalTicks(); got != 8 {
		t.Errorf("TotalTicks = %d, want 8", got)
	}
}

// APNG delays are the game's ticks over 30, and the loop byte decides
// whether the animation repeats.
func TestAPNGTimingAndLooping(t *testing.T) {
	for _, loop := range []bool{true, false} {
		var buf bytes.Buffer
		if err := timedSequence(loop, 0, 2, 65535).ToAPNG(FallbackPalette(), &buf); err != nil {
			t.Fatal(err)
		}
		actl := apngChunks(t, buf.Bytes(), "acTL")
		if len(actl) != 1 {
			t.Fatalf("got %d acTL chunks", len(actl))
		}
		if frames := binary.BigEndian.Uint32(actl[0]); frames != 3 {
			t.Errorf("num_frames = %d, want 3", frames)
		}
		plays := binary.BigEndian.Uint32(actl[0][4:])
		if (loop && plays != 0) || (!loop && plays != 1) {
			t.Errorf("loop=%v: num_plays = %d", loop, plays)
		}
		fctl := apngChunks(t, buf.Bytes(), "fcTL")
		for i, want := range []uint16{1, 2, 65535} {
			num := binary.BigEndian.Uint16(fctl[i][20:])
			den := binary.BigEndian.Uint16(fctl[i][22:])
			if num != want || den != 30 {
				t.Errorf("frame %d delay = %d/%d, want %d/30", i, num, den, want)
			}
		}
		if n := len(apngChunks(t, buf.Bytes(), "IDAT")); n != 1 {
			t.Errorf("got %d IDAT chunks, want 1", n)
		}
		if n := len(apngChunks(t, buf.Bytes(), "fdAT")); n != 2 {
			t.Errorf("got %d fdAT chunks, want 2", n)
		}
	}
}

// GIF delays keep the game's overall speed: 1-tick frames alternate 3 and 4
// hundredths so every three frames take exactly 10.
func TestGIFTimingAndLooping(t *testing.T) {
	g, err := timedSequence(true, 1, 1, 1, 0, 2).ToGIF(FallbackPalette())
	if err != nil {
		t.Fatal(err)
	}
	want := []int{3, 4, 3, 3, 7}
	for i, d := range g.Delay {
		if d != want[i] {
			t.Errorf("delays = %v, want %v", g.Delay, want)
			break
		}
	}
	if g.LoopCount != 0 {
		t.Errorf("looping sequence: LoopCount = %d, want 0 (forever)", g.LoopCount)
	}
	for i, d := range g.Disposal {
		if d != gif.DisposalBackground {
			t.Errorf("frame %d disposal = %d, want background", i, d)
		}
	}

	once, err := timedSequence(false, 3, 3).ToGIF(FallbackPalette())
	if err != nil {
		t.Fatal(err)
	}
	if once.LoopCount != -1 {
		t.Errorf("non-looping sequence: LoopCount = %d, want -1 (play once)", once.LoopCount)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, once); err != nil {
		t.Fatal(err)
	}
}

// Canvas bounds use full-width integers: frames at opposite ends of the
// int16 origin range give a wide canvas, not a wrapped one.
func TestCanvasBoundsDoNotWrap(t *testing.T) {
	a := &Frame{Width: 3, Height: 1, OriginX: 32767, Pixels: []byte{1, 2, 3}}
	b := &Frame{Width: 3, Height: 1, OriginX: -32768, Pixels: []byte{4, 5, 6}}
	g, err := (&Sequence{Frames: []*Frame{a, b}}).ToGIF(FallbackPalette())
	if err != nil {
		t.Fatal(err)
	}
	if g.Config.Width != 65538 || g.Config.Height != 1 {
		t.Fatalf("canvas %dx%d, want 65538x1", g.Config.Width, g.Config.Height)
	}
	if got := g.Image[0].Pix[:3]; !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("frame 0 at the left edge: %v", got)
	}
	if got := g.Image[1].Pix[65535:]; !bytes.Equal(got, []byte{4, 5, 6}) {
		t.Errorf("frame 1 at the right edge: %v", got)
	}

	tall := &Frame{Width: 2000, Height: 1, OriginX: 32767, OriginY: 32767, Pixels: make([]byte, 2000)}
	far := &Frame{Width: 2000, Height: 1, OriginX: -32768, OriginY: -32768, Pixels: make([]byte, 2000)}
	if _, err := (&Sequence{Frames: []*Frame{tall, far}}).ToGIF(nil); err == nil {
		t.Error("expected an error for a canvas of billions of pixels")
	}
}
