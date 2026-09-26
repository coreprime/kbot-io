package tsf

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder for retail TSF .jpg layers
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ImageResolver loads a layer image referenced by a TSF Filename and returns it
// as a non-premultiplied RGBA image.
type ImageResolver interface {
	Resolve(filename string) (*image.NRGBA, error)
}

// Compile builds a binary TAF from a TSF document, loading each layer image
// through res. The document's first section is treated as the animation.
//
// The per-frame pixel format is taken from a "Format" assignment in the frame
// section (defaulting to ARGB4444), the duration from "Delay", and the
// placement from the layer's "AnchorX"/"AnchorY". A "Flags" assignment, when
// present, restores the preserved frame-info byte so a decompiled animation can
// be recompiled byte-for-byte. Each value must fit its binary field (Delay
// 0-4294967295, AnchorX and AnchorY -32768-32767, Flags 0-255); a value
// outside that range is an error rather than being wrapped.
func Compile(doc *Document, res ImageResolver) (*TAF, error) {
	if doc == nil || len(doc.Sections) == 0 {
		return nil, fmt.Errorf("tsf: document has no animation section")
	}
	anim := doc.Sections[0]
	frames := anim.Subsections()
	if len(frames) == 0 {
		return nil, fmt.Errorf("tsf: animation [%s] has no frames", anim.Name)
	}

	taf := &TAF{Name: anim.Name, Frames: make([]*Frame, 0, len(frames))}
	if raw, ok := anim.Get("RawName"); ok {
		decoded, err := hex.DecodeString(raw)
		if err != nil || len(decoded) != nameFieldLen {
			return nil, fmt.Errorf("tsf: invalid RawName %q", raw)
		}
		copy(taf.nameField[:], decoded)
		taf.rawNameSet = true
	}
	for i, fs := range frames {
		frame, err := compileFrame(fs, res)
		if err != nil {
			return nil, fmt.Errorf("tsf: frame %d ([%s]): %w", i, fs.Name, err)
		}
		taf.Frames = append(taf.Frames, frame)
	}
	return taf, nil
}

func compileFrame(fs *Section, res ImageResolver) (*Frame, error) {
	layers := fs.Subsections()
	if len(layers) != 1 {
		return nil, fmt.Errorf("expected exactly one layer, found %d", len(layers))
	}
	layer := layers[0]

	filename, ok := layer.Get("Filename")
	if !ok {
		return nil, fmt.Errorf("layer [%s] has no Filename", layer.Name)
	}
	img, err := res.Resolve(filename)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", filename, err)
	}
	w := img.Rect.Dx()
	h := img.Rect.Dy()
	if w <= 0 || h <= 0 || w > 0xFFFF || h > 0xFFFF {
		return nil, fmt.Errorf("image %q has unusable dimensions %dx%d", filename, w, h)
	}

	format := FormatARGB4444
	if v, ok := fs.Get("Format"); ok {
		format, err = parsePixelFormat(v)
		if err != nil {
			return nil, err
		}
	}

	duration, err := getInt(fs, "Delay", 0, math.MaxUint32)
	if err != nil {
		return nil, err
	}
	originX, err := getInt(layer, "AnchorX", math.MinInt16, math.MaxInt16)
	if err != nil {
		return nil, err
	}
	originY, err := getInt(layer, "AnchorY", math.MinInt16, math.MaxInt16)
	if err != nil {
		return nil, err
	}
	flagB, err := getInt(fs, "Flags", 0, math.MaxUint8)
	if err != nil {
		return nil, err
	}

	pixels, err := PixelsFromNRGBA(img, format)
	if err != nil {
		return nil, err
	}

	return &Frame{
		Width:    uint16(w),
		Height:   uint16(h),
		OriginX:  int16(originX),
		OriginY:  int16(originY),
		Format:   format,
		Duration: uint32(duration),
		Pixels:   pixels,
		flagB:    uint8(flagB),
	}, nil
}

// getInt reads an optional integer assignment (0 when absent) and checks it
// against the range of the binary field it is stored in.
func getInt(s *Section, key string, lo, hi int64) (int64, error) {
	v, ok := s.Get(key)
	if !ok {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s = %q: %w", key, v, err)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s = %d is outside the range %d to %d", key, n, lo, hi)
	}
	return n, nil
}

// MemoryResolver resolves filenames against an in-memory map of PNG bytes.
type MemoryResolver map[string][]byte

// NewMemoryResolver builds a MemoryResolver from decompiled image files.
func NewMemoryResolver(files []ImageFile) MemoryResolver {
	m := make(MemoryResolver, len(files))
	for _, f := range files {
		m[f.Name] = f.Data
	}
	return m
}

// Resolve implements ImageResolver.
func (m MemoryResolver) Resolve(filename string) (*image.NRGBA, error) {
	data, ok := m[filename]
	if !ok {
		return nil, fmt.Errorf("image %q not found", filename)
	}
	return decodeNRGBA(filename, data)
}

// DirResolver resolves filenames against a directory on disk. Lookups are
// case-insensitive to match the game's tolerant filename handling, and a
// backslash separates directories as in the game's own paths. Names are
// confined to the directory: absolute paths and names that climb out of it
// with ".." are rejected, and symbolic links cannot lead outside it. An empty
// DirResolver resolves against the working directory.
type DirResolver string

// Resolve implements ImageResolver.
func (d DirResolver) Resolve(filename string) (*image.NRGBA, error) {
	name, err := localName(filename)
	if err != nil {
		return nil, err
	}
	dir := string(d)
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		if alt, ok := findCaseInsensitive(root, name); ok {
			data, err = root.ReadFile(alt)
		}
	}
	if err != nil {
		return nil, err
	}
	return decodeImageFile(filename, data)
}

// localName converts a layer filename into a relative path inside the
// resolver's directory, treating backslashes as separators.
func localName(filename string) (string, error) {
	name := filepath.FromSlash(strings.ReplaceAll(filename, "\\", "/"))
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("image %q is outside the image directory", filename)
	}
	return filepath.Clean(name), nil
}

// findCaseInsensitive resolves name one path element at a time, matching each
// element against the directory entries without regard to ASCII case.
func findCaseInsensitive(root *os.Root, name string) (string, bool) {
	dir := "."
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i, part := range parts {
		f, err := root.Open(dir)
		if err != nil {
			return "", false
		}
		entries, err := f.ReadDir(-1)
		_ = f.Close()
		if err != nil {
			return "", false
		}
		found := ""
		for _, e := range entries {
			if i == len(parts)-1 && e.IsDir() {
				continue
			}
			if equalFold(e.Name(), part) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", false
		}
		dir = filepath.Join(dir, found)
	}
	return dir, true
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// MaxImagePixels is the largest layer image, in pixels, the resolvers
// decode. It is checked against the image header before any pixels are
// decoded; a TAF frame is also limited to 65535 pixels on each side.
const MaxImagePixels = 1 << 24

// checkImageSize rejects images too large for a TAF frame or the decode
// budget, reading only the image header.
func checkImageSize(filename string, data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode %q: %w", filename, err)
	}
	if cfg.Width > 0xFFFF || cfg.Height > 0xFFFF || int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return fmt.Errorf("image %q is %dx%d, larger than a TAF frame allows (%d pixels at most)",
			filename, cfg.Width, cfg.Height, MaxImagePixels)
	}
	return nil
}

// decodeImageFile decodes PNG or JPEG (the formats referenced by retail TSF)
// into a non-premultiplied RGBA image.
func decodeImageFile(filename string, data []byte) (*image.NRGBA, error) {
	if err := checkImageSize(filename, data); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %q: %w", filename, err)
	}
	return toNRGBA(img), nil
}

func decodeNRGBA(filename string, data []byte) (*image.NRGBA, error) {
	if err := checkImageSize(filename, data); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return toNRGBA(img), nil
}

// toNRGBA returns img as *image.NRGBA. When img already is one it is returned
// unchanged, preserving its exact pixel bytes (including color under fully
// transparent pixels).
func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}
