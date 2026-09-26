package pal

import (
	"bytes"
	"fmt"

	"github.com/coreprime/kbot-io/formats/pcx"
)

// Source says where LoadNamed found a palette.
type Source int

const (
	// SourcePAL: the palette came from palettes/<name>.pal.
	SourcePAL Source = iota + 1
	// SourcePCX: the .pal was missing or empty, so the palette came from
	// palettes/<name>.pcx.
	SourcePCX
)

// String returns "pal" or "pcx".
func (s Source) String() string {
	switch s {
	case SourcePAL:
		return "pal"
	case SourcePCX:
		return "pcx"
	default:
		return fmt.Sprintf("Source(%d)", int(s))
	}
}

// FromPCX builds a palette from a PCX file the way the game does when a .PAL
// is missing or empty: the colours are the file's last 768 bytes, whether or
// not a 0x0C marker precedes them. The PCX must be one the game loads
// (version 5, valid bounds, at least 896 bytes); pixel data that ends early
// does not matter. The fourth byte of each entry is zero.
func FromPCX(data []byte) (*Palette, error) {
	r, err := pcx.LoadFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("palette from PCX: %w", err)
	}
	for _, issue := range r.Compat().Issues {
		if issue.Severity == pcx.CompatError {
			return nil, fmt.Errorf("palette from PCX: %w: %s", pcx.ErrGameRejects, issue.Message)
		}
	}
	colors := data[len(data)-pcx.ColorMapSize:]
	entries := make([]byte, FileSize)
	for i := 0; i < EntryCount; i++ {
		copy(entries[i*4:i*4+3], colors[i*3:i*3+3])
	}
	return fromEntries(entries), nil
}

// LoadNamed loads the palette called name (for example "palette" or "guipal")
// the way the game does, reading files through read (such as a VFS's
// ReadFile):
//
//   - palettes/<name>.pal of 1,024 bytes or more gives its first 1,024
//     bytes;
//   - a .pal of 1 to 1,023 bytes fails with ErrShort, without a fallback;
//   - a missing (read fails) or empty .pal falls back to FromPCX on
//     palettes/<name>.pcx.
//
// It reports which file the palette came from. After a PCX fallback TA 3.1c
// also rebuilt the palette's lookup tables, so tables stored next to the old
// .pal no longer match; this function changes nothing on disk.
func LoadNamed(read func(path string) ([]byte, error), name string) (*Palette, Source, error) {
	palPath := "palettes/" + name + ".pal"
	data, palErr := read(palPath)
	if palErr == nil && len(data) > 0 {
		p, err := LoadFromBytes(data)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", palPath, err)
		}
		return p, SourcePAL, nil
	}
	if palErr == nil {
		palErr = ErrEmpty
	}

	pcxPath := "palettes/" + name + ".pcx"
	data, err := read(pcxPath)
	if err != nil {
		return nil, 0, fmt.Errorf("load palette %q: %s: %w; %s: %w", name, palPath, palErr, pcxPath, err)
	}
	p, err := FromPCX(data)
	if err != nil {
		return nil, 0, fmt.Errorf("load palette %q: %s: %w; %s: %w", name, palPath, palErr, pcxPath, err)
	}
	return p, SourcePCX, nil
}
