package pcx

import "fmt"

// CompatSeverity grades a CompatIssue.
type CompatSeverity int

const (
	// CompatWarning: the game loads the file but draws it differently from
	// a standard PCX reader (or from what its author intended).
	CompatWarning CompatSeverity = iota + 1
	// CompatError: the game refuses to load the file.
	CompatError
)

// String returns "warning" or "error".
func (s CompatSeverity) String() string {
	switch s {
	case CompatWarning:
		return "warning"
	case CompatError:
		return "error"
	default:
		return fmt.Sprintf("CompatSeverity(%d)", int(s))
	}
}

// CompatCode identifies the kind of a CompatIssue. The values are stable and
// safe to match on.
type CompatCode string

const (
	// CompatVersion: the version byte is not 5, so the game refuses the file.
	CompatVersion CompatCode = "version"
	// CompatBounds: XMax or YMax is below its minimum, so the game refuses
	// the file.
	CompatBounds CompatCode = "bounds"
	// CompatShortFile: the file cannot hold the header and the 768-byte
	// colour map, so the game refuses it.
	CompatShortFile CompatCode = "short-file"
	// CompatDepth: the image is not 8-bit single-plane; the game decodes it
	// as if it were.
	CompatDepth CompatCode = "depth"
	// CompatEncoding: the encoding byte is not 1 (RLE); the game run-length
	// decodes it anyway.
	CompatEncoding CompatCode = "encoding"
	// CompatBytesPerLine: BytesPerLine differs from the width; the game
	// ignores it and decodes width bytes per row.
	CompatBytesPerLine CompatCode = "bytes-per-line"
	// CompatPaletteMarker: no 0x0C marker precedes the last 768 bytes; the
	// game uses them as the palette anyway.
	CompatPaletteMarker CompatCode = "palette-marker"
	// CompatTruncated: the pixel data ends before the last row as the game
	// decodes it.
	CompatTruncated CompatCode = "truncated"
	// CompatRowsInPalette: the rows, as the game decodes them, run into the
	// trailing colour map, so the last rows show palette bytes.
	CompatRowsInPalette CompatCode = "rows-in-palette"
)

// CompatIssue is one way a file departs from what TA 3.1c expects.
type CompatIssue struct {
	Code     CompatCode
	Severity CompatSeverity
	// Message describes the problem and what the game does about it.
	Message string
}

// String returns the severity and message, e.g. "error: version 3: ...".
func (i CompatIssue) String() string {
	return i.Severity.String() + ": " + i.Message
}

// CompatReport lists the issues Compat found, errors first.
type CompatReport struct {
	Issues []CompatIssue
}

// GameLoads reports whether TA 3.1c loads the file (no CompatError issue).
func (c CompatReport) GameLoads() bool {
	for _, issue := range c.Issues {
		if issue.Severity == CompatError {
			return false
		}
	}
	return true
}

// OK reports whether the file has no issues at all: the game loads it and
// draws it exactly as a standard PCX reader does.
func (c CompatReport) OK() bool {
	return len(c.Issues) == 0
}

// Has reports whether the report contains an issue with the given code.
func (c CompatReport) Has(code CompatCode) bool {
	for _, issue := range c.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

// Compat checks the file against the rules TA 3.1c applies when it loads a
// PCX (see the package documentation) and reports every departure. It does
// not change how Decode behaves.
func (r *Reader) Compat() CompatReport {
	var rep CompatReport
	add := func(code CompatCode, sev CompatSeverity, format string, args ...any) {
		rep.Issues = append(rep.Issues, CompatIssue{Code: code, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	h := r.header
	width, height := r.Width(), r.Height()

	// Errors are checked first so they lead the report.
	if h.Version != GameVersion {
		add(CompatVersion, CompatError,
			"version %d: the game only loads version %d PCX files", h.Version, GameVersion)
	}
	boundsOK := width > 0 && height > 0
	if !boundsOK {
		add(CompatBounds, CompatError,
			"bounds XMin=%d XMax=%d YMin=%d YMax=%d are inverted: the game refuses the image",
			h.XMin, h.XMax, h.YMin, h.YMax)
	}
	longEnough := len(r.rawData) >= MinGameFileSize
	if !longEnough {
		add(CompatShortFile, CompatError,
			"file is %d bytes: the game needs at least %d (the header plus the 768-byte colour map)",
			len(r.rawData), MinGameFileSize)
	}

	if h.BitsPerPixel != 8 || h.NumPlanes != 1 {
		add(CompatDepth, CompatWarning,
			"%d-bit, %d-plane image: the game decodes every PCX as 8-bit single-plane, so this one is drawn as garbage",
			h.BitsPerPixel, h.NumPlanes)
	}
	if h.Encoding != encodingRLE {
		add(CompatEncoding, CompatWarning,
			"encoding %d: the game always run-length decodes the pixel data", h.Encoding)
	}
	if boundsOK && int(h.BytesPerLine) != width {
		add(CompatBytesPerLine, CompatWarning,
			"BytesPerLine is %d but the width is %d: the game ignores BytesPerLine and decodes %d bytes per row, so the rows shift",
			h.BytesPerLine, width, width)
	}
	if longEnough && !r.embedded {
		add(CompatPaletteMarker, CompatWarning,
			"no 0x0C marker before the last 768 bytes: the game still uses those bytes as the palette, while standard decoding falls back to the default TA palette")
	}

	if boundsOK && longEnough && r.checkExtent() == nil {
		end, complete := r.gameRowsEnd()
		mapStart := len(r.rawData) - ColorMapSize
		if r.embedded {
			mapStart--
		}
		switch {
		case !complete:
			add(CompatTruncated, CompatWarning,
				"the pixel data ends before the last row: the game keeps drawing from the last byte it read")
		case end > mapStart:
			add(CompatRowsInPalette, CompatWarning,
				"the rows, decoded %d bytes each, run %d bytes into the colour map: the game draws those palette bytes as pixels",
				width, end-mapStart)
		}
	}

	return rep
}
