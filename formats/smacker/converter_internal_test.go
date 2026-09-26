package smacker

import (
	"slices"
	"testing"
)

// argAfter returns the argument following flag, or "" when flag is absent.
func argAfter(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func TestDisplayFilter(t *testing.T) {
	cases := []struct {
		flags uint32
		fill  InterlaceFill
		want  string
	}{
		{0, InterlaceBlackLines, "setsar=1"},
		{FlagInterlaced, InterlaceBlackLines, "format=yuv444p,pad=iw:ih*2:0:0:black,il=l=i:c=i:a=i,setsar=1"},
		{FlagInterlaced, InterlaceLineDouble, "scale=iw:ih*2:flags=neighbor,setsar=1"},
		{FlagDoubled, InterlaceBlackLines, "scale=iw:ih*2:flags=neighbor,setsar=1"},
		{FlagInterlaced | FlagDoubled, InterlaceBlackLines, "setsar=1"},
	}
	for _, c := range cases {
		h := &Header{Flags: c.flags}
		if got := DisplayFilter(h, c.fill); got != c.want {
			t.Errorf("flags %#x fill %d: DisplayFilter = %q, want %q", c.flags, c.fill, got, c.want)
		}
	}
}

func TestMP4Args(t *testing.T) {
	h := &Header{Frames: 599, FrameRate: -3333, Flags: FlagInterlaced}
	args := mp4Args(h, "in.zrb", "out.mp4", MP4Options{})
	if got := argAfter(args, "-i"); got != "file:in.zrb" {
		t.Errorf("-i %q, want file:in.zrb", got)
	}
	if got := args[len(args)-1]; got != "file:out.mp4" {
		t.Errorf("output %q, want file:out.mp4", got)
	}
	if got := argAfter(args, "-vf"); got != DisplayFilter(h, InterlaceBlackLines) {
		t.Errorf("-vf %q", got)
	}
	if got := argAfter(args, "-frames:v"); got != "599" {
		t.Errorf("-frames:v %q, want 599 (the header count, without any ring frame)", got)
	}
	if slices.Contains(args, "-r") {
		t.Errorf("unexpected -r for a stored frame rate: %v", args)
	}
	if !slices.Contains(args, "-nostdin") {
		t.Errorf("missing -nostdin: %v", args)
	}

	// Line doubling for interlaced movies, and the stored height.
	if got := argAfter(mp4Args(h, "a", "b", MP4Options{Interlace: InterlaceLineDouble}), "-vf"); got != "scale=iw:ih*2:flags=neighbor,setsar=1" {
		t.Errorf("line-double -vf %q", got)
	}
	if got := mp4Args(h, "a", "b", MP4Options{StoredHeight: true}); slices.Contains(got, "-vf") {
		t.Errorf("StoredHeight still filters: %v", got)
	}

	// A zero frame rate is timed at DefaultFrameRate, given before the input.
	h0 := &Header{Frames: 10}
	args = mp4Args(h0, "in.smk", "out.mp4", MP4Options{})
	ri, ii := slices.Index(args, "-r"), slices.Index(args, "-i")
	if ri < 0 || ri > ii || args[ri+1] != "15" {
		t.Errorf("zero rate: want -r 15 before -i, got %v", args)
	}
}

// TestFFmpegPathsNotOptions checks that paths starting with "-" or holding
// ":" reach FFmpeg behind the file: prefix, so they cannot be read as
// options or protocols.
func TestFFmpegPathsNotOptions(t *testing.T) {
	h := &Header{Frames: 1, FrameRate: 100}
	for _, c := range []struct {
		args    []string
		in, out string
	}{
		{mp4Args(h, "-evil.smk", "-y.mp4", MP4Options{}), "-evil.smk", "-y.mp4"},
		{fromMP4Args("-evil.mp4", "http:out.smk"), "-evil.mp4", "http:out.smk"},
	} {
		if got := argAfter(c.args, "-i"); got != "file:"+c.in {
			t.Errorf("input passed as %q, want %q", got, "file:"+c.in)
		}
		if got := c.args[len(c.args)-1]; got != "file:"+c.out {
			t.Errorf("output passed as %q, want %q", got, "file:"+c.out)
		}
		if slices.Contains(c.args, c.in) || slices.Contains(c.args, c.out) {
			t.Errorf("a bare path reached FFmpeg: %v", c.args)
		}
	}
}

func TestNoSmackerWriterMessages(t *testing.T) {
	for _, out := range []string{
		"[out#0 @ 0x1] Unable to choose an output format for 'out.smk'; use a standard extension for the filename or specify the format manually.",
		"Unknown encoder 'smackvid'",
		"[NULL @ 0x1] Unable to find a suitable output format for 'out.smk'",
	} {
		if !noSmackerWriter(out) {
			t.Errorf("noSmackerWriter(%q) = false", out)
		}
	}
	if noSmackerWriter("in.mp4: No such file or directory") {
		t.Error("noSmackerWriter matched an unrelated error")
	}
}

func TestListingHas(t *testing.T) {
	encoders := `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 A....D aac                  AAC (Advanced Audio Coding)
`
	muxers := `File formats:
 D. = Demuxing supported
 .E = Muxing supported
 ---
  E mp4             MP4 (MPEG-4 Part 14)
  E matroska,webm   Matroska
`
	if listingHas(encoders, "smackvid") || listingHas(muxers, "smk") {
		t.Error("found a Smacker writer in listings without one")
	}
	if !listingHas(encoders, "libx264") || !listingHas(muxers, "webm") || !listingHas(muxers, "mp4") {
		t.Error("listingHas missed a listed name")
	}
	if !listingHas(encoders+" V....D smackvid  Smacker video\n", "smackvid") {
		t.Error("listingHas missed smackvid")
	}
}
