package tsf

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tinyPNG encodes a 2×1 image.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// oneFrameTSF is a single-frame animation with the given frame and layer
// assignments.
func oneFrameTSF(frame, layer string) string {
	return "[Anim]\n{\n\t[Frame0]\n\t{\n" + frame + "\t\t[Layer0]\n\t\t{\n" + layer +
		"\t\t\tFilename = a.png;\n\t\t}\n\t}\n}\n"
}

func TestCompileRangeChecksFields(t *testing.T) {
	res := MemoryResolver{"a.png": tinyPNG(t)}
	cases := []struct {
		name, frame, layer string
		ok                 bool
	}{
		{"anchor x too large", "", "\t\t\tAnchorX = 40000;\n", false},
		{"anchor y too small", "", "\t\t\tAnchorY = -32769;\n", false},
		{"negative delay", "\t\tDelay = -1;\n", "", false},
		{"delay too large", "\t\tDelay = 4294967296;\n", "", false},
		{"flags too large", "\t\tFlags = 256;\n", "", false},
		{"extremes", "\t\tDelay = 4294967295;\n\t\tFlags = 255;\n", "\t\t\tAnchorX = -32768;\n\t\t\tAnchorY = 32767;\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := ParseTSF(oneFrameTSF(c.frame, c.layer))
			if err != nil {
				t.Fatalf("ParseTSF: %v", err)
			}
			taf, err := Compile(doc, res)
			if !c.ok {
				if err == nil || !strings.Contains(err.Error(), "outside the range") {
					t.Fatalf("got %v, want a range error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			f := taf.Frames[0]
			if f.OriginX != -32768 || f.OriginY != 32767 || f.Duration != math.MaxUint32 || f.FlagByte() != 255 {
				t.Fatalf("frame = %+v", f)
			}
		})
	}
}

func TestSerializedSizeLimit(t *testing.T) {
	if n, err := serializedSize(88, 100); err != nil || n != 188 {
		t.Fatalf("serializedSize = %d, %v", n, err)
	}
	if _, err := serializedSize(88, math.MaxUint32); err == nil {
		t.Fatal("a file over 4 GiB was accepted")
	}
}

func TestDirResolverStaysInsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "anims")
	if err := os.MkdirAll(filepath.Join(root, "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := tinyPNG(t)
	for _, p := range []string{filepath.Join(root, "Sub", "Img.PNG"), filepath.Join(base, "secret.png")} {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := DirResolver(root)

	// Backslashes separate directories and case does not matter.
	if _, err := res.Resolve(`sub\img.png`); err != nil {
		t.Fatalf("Resolve(sub\\img.png): %v", err)
	}
	for _, name := range []string{"../secret.png", `..\secret.png`, "Sub/../../secret.png", filepath.Join(base, "secret.png")} {
		if _, err := res.Resolve(name); err == nil {
			t.Errorf("Resolve(%q) escaped the directory", name)
		}
	}

	// A symbolic link inside the directory cannot lead outside it.
	if err := os.Symlink(filepath.Join(base, "secret.png"), filepath.Join(root, "link.png")); err == nil {
		if _, err := res.Resolve("link.png"); err == nil {
			t.Error("Resolve followed a symbolic link out of the directory")
		}
	}
}

func TestEmptyDirResolverUsesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "img.png"), tinyPNG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	res := DirResolver("")
	if _, err := res.Resolve("IMG.png"); err != nil {
		t.Fatalf("Resolve(IMG.png): %v", err)
	}
	if _, err := res.Resolve("../img.png"); err == nil {
		t.Error("Resolve(../img.png) escaped the working directory")
	}
}

// pngHeader returns a PNG signature and IHDR chunk claiming w×h RGBA pixels,
// enough for image.DecodeConfig.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 17)
	copy(ihdr, "IHDR")
	binary.BigEndian.PutUint32(ihdr[4:], w)
	binary.BigEndian.PutUint32(ihdr[8:], h)
	ihdr[12], ihdr[13] = 8, 6 // 8-bit RGBA
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

func TestResolversLimitImageSize(t *testing.T) {
	for _, dims := range [][2]uint32{{70000, 1}, {5000, 5000}} {
		res := MemoryResolver{"big.png": pngHeader(dims[0], dims[1])}
		_, err := res.Resolve("big.png")
		if err == nil || !strings.Contains(err.Error(), "larger than a TAF frame allows") {
			t.Errorf("%dx%d: got %v, want a size error", dims[0], dims[1], err)
		}
	}
}
