package smacker

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// FFmpegAvailable reports whether an ffmpeg binary is on the PATH.
func FFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// InterlaceFill selects how an interlaced movie (HeightInterlaced) is drawn
// at its display height.
type InterlaceFill int

const (
	// InterlaceBlackLines draws each stored line followed by a black line,
	// as the game shows interlaced movies.
	InterlaceBlackLines InterlaceFill = iota
	// InterlaceLineDouble draws each stored line twice, giving a full
	// picture without the black lines.
	InterlaceLineDouble
)

// MP4Options adjusts ConvertToMP4WithOptions. The zero value converts the
// movie as the game shows it.
type MP4Options struct {
	// Interlace selects how interlaced movies fill every second line.
	Interlace InterlaceFill
	// StoredHeight keeps the stored frame height instead of converting to
	// the display height.
	StoredHeight bool
}

// DisplayFilter returns the FFmpeg video filter chain that turns the
// decoded frames of a movie with header h into what the game shows: the
// display height (see Header.DisplayHeight), with fill choosing how an
// interlaced movie fills its extra lines, and square pixels, so a 640x240
// interlaced movie becomes a 4:3 640x480 picture. The chain can be extended
// with further filters after a comma.
func DisplayFilter(h *Header, fill InterlaceFill) string {
	switch h.HeightMode() {
	case HeightInterlaced:
		if fill == InterlaceBlackLines {
			// Pad the frame with black to twice its height, then interleave
			// the two halves so stored lines land on even rows and black on
			// odd rows. yuv444p keeps chroma per row so no colour bleeds
			// into the black lines.
			return "format=yuv444p,pad=iw:ih*2:0:0:black,il=l=i:c=i:a=i,setsar=1"
		}
		return "scale=iw:ih*2:flags=neighbor,setsar=1"
	case HeightDoubled:
		return "scale=iw:ih*2:flags=neighbor,setsar=1"
	default:
		// FFmpeg marks some movies as having tall pixels; the game draws
		// every movie with square pixels.
		return "setsar=1"
	}
}

// ffmpegPath makes a file path safe to pass to FFmpeg as an input or output:
// the file: protocol prefix stops a name starting with "-" being read as an
// option and a name containing ":" being read as another protocol.
func ffmpegPath(p string) string {
	return "file:" + p
}

// mp4Args builds the FFmpeg arguments for ConvertToMP4WithOptions.
//
// The output stops after the header's frame count, so a ring frame (which
// FFmpeg decodes as one more frame) is not shown, as in the game. A zero
// frame rate, which FFmpeg cannot time, is replaced by DefaultFrameRate so
// the MP4 matches Reader.FrameRate.
func mp4Args(h *Header, smkPath, mp4Path string, opts MP4Options) []string {
	args := []string{"-hide_banner", "-nostdin"}
	if h.FrameRate == 0 {
		args = append(args, "-r", strconv.FormatFloat(DefaultFrameRate, 'f', -1, 64))
	}
	args = append(args, "-i", ffmpegPath(smkPath))
	if !opts.StoredHeight {
		args = append(args, "-vf", DisplayFilter(h, opts.Interlace))
	}
	if h.Frames > 0 {
		args = append(args, "-frames:v", strconv.FormatUint(uint64(h.Frames), 10))
	}
	// -c:v libx264 -preset fast -crf 18: visually lossless H.264.
	// -c:a aac -b:a 192k: AAC audio. -y: overwrite the output.
	return append(args,
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "18",
		"-c:a", "aac",
		"-b:a", "192k",
		"-y",
		ffmpegPath(mp4Path),
	)
}

// ConvertToMP4 converts a Smacker video file to MP4 (H.264 and AAC) using
// FFmpeg, as the game shows it: at the display height, with the black
// lines of an interlaced movie, square pixels, and the header's frame
// count. It is ConvertToMP4WithOptions with zero options.
func ConvertToMP4(smkPath, mp4Path string) error {
	return ConvertToMP4WithOptions(smkPath, mp4Path, MP4Options{})
}

// ConvertToMP4WithOptions converts a Smacker video file to MP4 (H.264 and
// AAC) using FFmpeg, which decodes Smacker natively. The header is read
// first to find the display height, frame count and frame rate; see
// MP4Options for the choices.
func ConvertToMP4WithOptions(smkPath, mp4Path string, opts MP4Options) error {
	if !FFmpegAvailable() {
		return fmt.Errorf("ffmpeg not found in PATH\nPlease install: brew install ffmpeg (macOS) or apt-get install ffmpeg (Linux)")
	}

	r, err := OpenReader(smkPath)
	if err != nil {
		return err
	}
	h := r.Header()
	_ = r.Close()

	cmd := exec.Command("ffmpeg", mp4Args(h, smkPath, mp4Path, opts)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg conversion failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// ErrNoSmackerWriter reports that MP4 cannot be converted to Smacker:
// FFmpeg decodes Smacker but has no Smacker encoder or muxer, and this
// package has no Smacker writer. SMK2 movies for TA are made with RAD's
// Smacker tools.
var ErrNoSmackerWriter = errors.New("smacker: FFmpeg has no Smacker encoder or muxer, so MP4 cannot be converted to Smacker; " +
	"make SMK2 movies with RAD's Smacker tools")

// ConvertFromMP4 would convert an MP4 file to Smacker with FFmpeg's
// smackvid/smackaud encoders and smk muxer. Stock FFmpeg has none of them,
// so unless the installed FFmpeg lists both a smackvid encoder and an smk
// muxer, ConvertFromMP4 returns ErrNoSmackerWriter at once without
// converting anything. If FFmpeg is run and reports that it cannot encode
// or write Smacker, the result is also ErrNoSmackerWriter.
func ConvertFromMP4(mp4Path, smkPath string) error {
	if !FFmpegAvailable() {
		return fmt.Errorf("ffmpeg not found in PATH\nPlease install: brew install ffmpeg (macOS) or apt-get install ffmpeg (Linux)")
	}
	if ok, err := ffmpegCanWriteSmacker(); err == nil && !ok {
		return ErrNoSmackerWriter
	}

	cmd := exec.Command("ffmpeg", fromMP4Args(mp4Path, smkPath)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if noSmackerWriter(string(output)) {
			return ErrNoSmackerWriter
		}
		return fmt.Errorf("ffmpeg conversion failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// ffmpegCanWriteSmacker asks FFmpeg whether it has a smackvid encoder and
// an smk muxer.
func ffmpegCanWriteSmacker() (bool, error) {
	encoders, err := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-encoders").Output()
	if err != nil {
		return false, err
	}
	muxers, err := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-muxers").Output()
	if err != nil {
		return false, err
	}
	return listingHas(string(encoders), "smackvid") && listingHas(string(muxers), "smk"), nil
}

// listingHas reports whether an "ffmpeg -encoders" or "ffmpeg -muxers"
// listing names name. Each entry line is a flags column followed by the
// name, or by comma-separated names.
func listingHas(listing, name string) bool {
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, n := range strings.Split(f[1], ",") {
			if n == name {
				return true
			}
		}
	}
	return false
}

// noSmackerWriter reports whether FFmpeg's output says it has no Smacker
// encoder ("Unknown encoder") or no muxer for the .smk output ("Unable to
// choose an output format", or "Unable to find a suitable output format"
// from older versions).
func noSmackerWriter(output string) bool {
	for _, msg := range []string{
		"Unknown encoder",
		"Unable to choose an output format",
		"Unable to find a suitable output format",
	} {
		if strings.Contains(output, msg) {
			return true
		}
	}
	return false
}

// fromMP4Args builds the FFmpeg arguments for ConvertFromMP4.
func fromMP4Args(mp4Path, smkPath string) []string {
	return []string{
		"-hide_banner", "-nostdin",
		"-i", ffmpegPath(mp4Path),
		"-c:v", "smackvid", // Smacker video codec
		"-c:a", "smackaud", // Smacker audio codec
		"-y",
		ffmpegPath(smkPath),
	}
}

// StreamToMP4 converts a Smacker file to MP4 next to it (same name, .mp4
// extension) with ConvertToMP4 and returns the output path. Useful for web
// streaming, converting on demand.
func StreamToMP4(smkPath string) (string, error) {
	// Create temp output path
	ext := filepath.Ext(smkPath)
	mp4Path := strings.TrimSuffix(smkPath, ext) + ".mp4"

	if err := ConvertToMP4(smkPath, mp4Path); err != nil {
		return "", err
	}

	return mp4Path, nil
}
