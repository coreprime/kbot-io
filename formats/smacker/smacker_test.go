package smacker_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/smacker"
	"github.com/coreprime/kbot-io/testutil"
)

// TestKnownHeader pins the parsed fields of a shipped Cavedog cinematic so a
// regression in the byte layout is caught immediately.
func TestKnownHeader(t *testing.T) {
	path := testutil.UnpackedFile(t, "data", "1.zrb")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("sample not available: %v", err)
	}

	r, err := smacker.OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	if got := r.SignatureString(); got != "SMK2" {
		t.Errorf("signature = %q, want SMK2", got)
	}
	if got := r.Width(); got != 640 {
		t.Errorf("width = %d, want 640", got)
	}
	if got := r.Height(); got != 240 {
		t.Errorf("height = %d, want 240", got)
	}
	// Flag 2: the game shows the 240 stored lines as 480, every second one black.
	if got := r.HeightMode(); got != smacker.HeightInterlaced {
		t.Errorf("height mode = %v, want interlaced", got)
	}
	if got := r.DisplayHeight(); got != 480 {
		t.Errorf("display height = %d, want 480", got)
	}
	if got := r.FrameCount(); got != 599 {
		t.Errorf("frames = %d, want 599", got)
	}
	// Stored as a sign-encoded value (-3333), so the decoded rate is ~30.003.
	if got := r.FrameRate(); got < 29.9 || got > 30.1 {
		t.Errorf("fps = %.4f, want ~30", got)
	}
	if !r.HasAudio() {
		t.Fatal("expected at least one audio track")
	}
	// One track: 22050 Hz, stereo, 8-bit, compressed (packed word 0xD0005622).
	tracks := r.AudioTracks()
	if len(tracks) != 1 {
		t.Fatalf("audio tracks = %+v, want exactly one", tracks)
	}
	if tr := tracks[0]; tr.Index != 0 || tr.SampleRate != 22050 || tr.Channels() != 2 ||
		tr.BitsPerSample() != 8 || !tr.Compressed {
		t.Errorf("track = %+v, want track 0 at 22050 Hz, stereo, 8-bit, compressed", tr)
	}
}

// TestParseAllVideos walks every Smacker file under data/ and asserts the
// header parses with sane geometry — exercising the parser across the full
// shipped corpus rather than a single hand-picked file.
func TestParseAllVideos(t *testing.T) {
	dir := testutil.UnpackedDir(t, "data")

	var seen int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".zrb" && ext != ".smk" {
			return nil
		}
		r, err := smacker.OpenReader(path)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			return nil
		}
		defer func() { _ = r.Close() }()

		// The shipped movies are either 640x240 interlaced or 640x304 as stored.
		if dh := r.DisplayHeight(); dh != 480 && dh != 304 {
			t.Errorf("%s: display height %d (stored %d, flags %#x)", filepath.Base(path), dh, r.Height(), r.Header().Flags)
		}
		if r.Width() <= 0 || r.Height() <= 0 {
			t.Errorf("%s: bad dimensions %dx%d", filepath.Base(path), r.Width(), r.Height())
		}
		if r.FrameCount() <= 0 {
			t.Errorf("%s: frame count %d", filepath.Base(path), r.FrameCount())
		}
		if r.FrameRate() <= 0 {
			t.Errorf("%s: frame rate %.2f", filepath.Base(path), r.FrameRate())
		}
		if err := r.Validate(smacker.DefaultLimits()); err != nil {
			t.Errorf("%s: Validate: %v", filepath.Base(path), err)
		}
		if r.Version() != 2 {
			t.Errorf("%s: version %d, want 2", filepath.Base(path), r.Version())
		}
		// The shipped movies store their payloads back to back after the
		// tables and trees, ending exactly at the end of the file.
		if st, err := os.Stat(path); err == nil {
			h := r.Header()
			if end := h.FrameDataOffset() + int64(h.FrameDataSize()); end != st.Size() {
				t.Errorf("%s: payloads end at %d, file is %d bytes", filepath.Base(path), end, st.Size())
			}
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if seen == 0 {
		t.Skip("no .zrb/.smk files found under data/")
	}
	t.Logf("parsed %d Smacker files", seen)
}

// TestRejectsNonSmacker confirms the parser refuses a file whose signature is
// not SMK2/SMK4 with a clear error rather than panicking or returning garbage.
func TestRejectsNonSmacker(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bogus.zrb")
	if err := os.WriteFile(tmp, []byte("NOPE-not-a-smacker-header-padding-bytes-0000"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if _, err := smacker.OpenReader(tmp); err == nil {
		t.Fatal("expected an error parsing a non-Smacker file")
	}
}

// TestInfo checks the human-readable summary contains the key fields.
func TestInfo(t *testing.T) {
	path := testutil.UnpackedFile(t, "data", "1.zrb")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("sample not available: %v", err)
	}
	r, err := smacker.OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	info := r.Info()
	for _, want := range []string{"Smacker Video File", "SMK2", "640x240", "Display: 640x480 (interlaced)",
		"Frames: 599", "Track 0: 22050 Hz, 2 channels, 8-bit, compressed"} {
		if !strings.Contains(info, want) {
			t.Errorf("Info() missing %q\n%s", want, info)
		}
	}
}

// TestConvertToMP4 converts the retail 1.zrb (640x240, interlaced) to MP4
// via FFmpeg and checks the result is what the game shows: 640x480 with
// square pixels, 599 frames, and every second line black. The source is
// given as a relative path starting with "-", which FFmpeg must not read as
// an option. It is skipped when ffmpeg is unavailable.
func TestConvertToMP4(t *testing.T) {
	if !smacker.FFmpegAvailable() {
		t.Skip("ffmpeg not on PATH — skipping conversion test")
	}
	src := testutil.UnpackedFile(t, "data", "1.zrb")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("sample not available: %v", err)
	}
	t.Chdir(t.TempDir())
	if err := os.Symlink(src, "-intro.zrb"); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := smacker.ConvertToMP4("-intro.zrb", "-intro.mp4"); err != nil {
		t.Fatalf("ConvertToMP4: %v", err)
	}
	info, err := os.Stat("-intro.mp4")
	if err != nil {
		t.Fatalf("output not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output MP4 is empty")
	}

	if got := videoStreamInfo(t, "-intro.mp4"); got != "640|480|1:1|599" {
		t.Errorf("video stream width|height|sar|frames = %s, want 640|480|1:1|599", got)
	}

	// Frame 45 is a bright desert scene: even rows carry it, odd rows are
	// black.
	rows := grayRows(t, "-intro.mp4", 45, 640, 480, "")
	var even, odd float64
	for y, m := range rows {
		if y%2 == 0 {
			even += m
		} else {
			odd += m
		}
	}
	even /= 240
	odd /= 240
	if even < 100 || odd > 8 {
		t.Errorf("mean luma: even rows %.1f, odd rows %.1f; want bright even rows and black odd rows", even, odd)
	}
}

// TestDisplayFilterLineDouble checks the line-doubling chain that callers
// can use for previews: FFmpeg accepts it and it fills every row.
func TestDisplayFilterLineDouble(t *testing.T) {
	if !smacker.FFmpegAvailable() {
		t.Skip("ffmpeg not on PATH")
	}
	src := testutil.UnpackedFile(t, "data", "1.zrb")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("sample not available: %v", err)
	}
	r, err := smacker.OpenReader(src)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	_ = r.Close()
	vf := smacker.DisplayFilter(r.Header(), smacker.InterlaceLineDouble)
	rows := grayRows(t, src, 45, 640, 480, vf)
	for y := 1; y < len(rows); y += 2 {
		if rows[y] < 100 {
			t.Fatalf("row %d mean luma %.1f; want every row filled", y, rows[y])
		}
	}
}

// TestConvertZeroFrameRate checks that a movie whose header frame rate is 0
// converts at DefaultFrameRate, matching Reader.FrameRate.
func TestConvertZeroFrameRate(t *testing.T) {
	if !smacker.FFmpegAvailable() {
		t.Skip("ffmpeg not on PATH")
	}
	data, err := os.ReadFile(testutil.UnpackedFile(t, "data", "1.zrb"))
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	copy(data[16:20], []byte{0, 0, 0, 0}) // frame rate 0
	dir := t.TempDir()
	in, out := filepath.Join(dir, "zero.smk"), filepath.Join(dir, "zero.mp4")
	if err := os.WriteFile(in, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := smacker.ConvertToMP4(in, out); err != nil {
		t.Fatalf("ConvertToMP4: %v", err)
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate", "-of", "csv=p=0", out)
	got, err := cmd.Output()
	if err != nil {
		t.Skipf("ffprobe: %v", err)
	}
	if strings.TrimSpace(string(got)) != "15/1" {
		t.Errorf("r_frame_rate = %s, want 15/1", strings.TrimSpace(string(got)))
	}
}

// videoStreamInfo returns "width|height|sar|frames" of the first video stream.
func videoStreamInfo(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,sample_aspect_ratio,nb_frames",
		"-of", "compact=p=0:nk=1", "file:"+path).Output()
	if err != nil {
		t.Skipf("ffprobe: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// grayRows decodes frame n of path (after the optional filter chain vf) as
// 8-bit luma and returns the mean of each of its h rows.
func grayRows(t *testing.T, path string, n, w, h int, vf string) []float64 {
	t.Helper()
	sel := fmt.Sprintf("select=eq(n\\,%d)", n)
	if vf != "" {
		sel = vf + "," + sel
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-i", "file:"+path,
		"-vf", sel, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
	if err != nil {
		t.Fatalf("decode frame %d: %v", n, err)
	}
	if len(out) != w*h {
		t.Fatalf("frame %d is %d bytes, want %dx%d", n, len(out), w, h)
	}
	rows := make([]float64, h)
	for y := range rows {
		sum := 0
		for _, v := range out[y*w : (y+1)*w] {
			sum += int(v)
		}
		rows[y] = float64(sum) / float64(w)
	}
	return rows
}

// TestConvertFromMP4FailsEarly checks that, with an FFmpeg that cannot
// write Smacker, ConvertFromMP4 returns ErrNoSmackerWriter before touching
// its input (which does not exist here).
func TestConvertFromMP4FailsEarly(t *testing.T) {
	if !smacker.FFmpegAvailable() {
		t.Skip("ffmpeg not on PATH")
	}
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Skipf("ffmpeg -encoders: %v", err)
	}
	if strings.Contains(string(out), " smackvid ") {
		t.Skip("this FFmpeg has a Smacker encoder")
	}
	dir := t.TempDir()
	err = smacker.ConvertFromMP4(filepath.Join(dir, "missing.mp4"), filepath.Join(dir, "out.smk"))
	if !errors.Is(err, smacker.ErrNoSmackerWriter) {
		t.Fatalf("ConvertFromMP4 = %v, want ErrNoSmackerWriter", err)
	}
	if !strings.Contains(err.Error(), "no Smacker encoder or muxer") {
		t.Errorf("message %q does not say FFmpeg cannot write Smacker", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "out.smk")); statErr == nil {
		t.Error("an output file was created")
	}
}
