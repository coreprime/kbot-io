package pal

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TableColumns is the width of every lookup table row: one entry per palette
// index.
const TableColumns = EntryCount

const (
	// AlphaTableRows is the number of rows in PALETTE.ALP.
	AlphaTableRows = 256
	// RampTableRows is the number of rows in PALETTE.SHD and PALETTE.LHT.
	RampTableRows = 32

	// AlphaTableSize is the size in bytes of PALETTE.ALP (256 × 256).
	AlphaTableSize = AlphaTableRows * TableColumns
	// ShadeTableSize is the size in bytes of PALETTE.SHD (32 × 256).
	ShadeTableSize = RampTableRows * TableColumns
	// LightTableSize is the size in bytes of PALETTE.LHT (32 × 256).
	LightTableSize = RampTableRows * TableColumns
)

// ErrTableSize is wrapped by the errors for a lookup table whose size is not
// the one its kind requires. The game rebuilds such a table from the palette
// instead of using it.
var ErrTableSize = errors.New("pal: wrong lookup table size")

// TableKind identifies one of the palette lookup tables.
type TableKind int

const (
	// AlphaTable is PALETTE.ALP: entry [a][b] is the index nearest the
	// average of colours a and b.
	AlphaTable TableKind = iota + 1
	// ShadeTable is PALETTE.SHD: row r maps each index to the colour nearest
	// that colour scaled by r × 0.06875.
	ShadeTable
	// LightTable is PALETTE.LHT: row r maps each index to the colour nearest
	// that colour scaled by 1 + r/30.
	LightTable
)

// Rows returns the number of 256-entry rows in a table of this kind, or 0
// for an unknown kind.
func (k TableKind) Rows() int {
	switch k {
	case AlphaTable:
		return AlphaTableRows
	case ShadeTable, LightTable:
		return RampTableRows
	default:
		return 0
	}
}

// Size returns the exact size in bytes of a table of this kind, or 0 for an
// unknown kind.
func (k TableKind) Size() int {
	return k.Rows() * TableColumns
}

// Ext returns the file extension of the kind (".alp", ".shd" or ".lht"), or
// "" for an unknown kind.
func (k TableKind) Ext() string {
	switch k {
	case AlphaTable:
		return ".alp"
	case ShadeTable:
		return ".shd"
	case LightTable:
		return ".lht"
	default:
		return ""
	}
}

// String returns a short name such as "alpha (.alp)".
func (k TableKind) String() string {
	switch k {
	case AlphaTable:
		return "alpha (.alp)"
	case ShadeTable:
		return "shade (.shd)"
	case LightTable:
		return "light (.lht)"
	default:
		return fmt.Sprintf("TableKind(%d)", int(k))
	}
}

// TableKindFromPath returns the table kind named by a path's extension
// (.alp, .shd or .lht, any case).
func TableKindFromPath(path string) (TableKind, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".alp":
		return AlphaTable, true
	case ".shd":
		return ShadeTable, true
	case ".lht":
		return LightTable, true
	default:
		return 0, false
	}
}

// Table is a palette lookup table (.ALP, .SHD or .LHT). Data holds exactly
// Kind.Size() bytes, row by row; each byte is a palette index.
type Table struct {
	Kind TableKind
	Data []byte
}

func checkTableSize(kind TableKind, n int) error {
	want := kind.Size()
	if want == 0 {
		return fmt.Errorf("pal: unknown lookup table kind %d", int(kind))
	}
	if n != want {
		return fmt.Errorf("%w: a %s table is %d bytes, got %d", ErrTableSize, kind, want, n)
	}
	return nil
}

// NewTable validates data as a table of the given kind and returns a Table
// holding a copy of it. The size must be exact (see ErrTableSize).
func NewTable(kind TableKind, data []byte) (*Table, error) {
	if err := checkTableSize(kind, len(data)); err != nil {
		return nil, err
	}
	return &Table{Kind: kind, Data: append([]byte(nil), data...)}, nil
}

// ReadTable reads a whole table of the given kind from r. The stream must hold
// exactly kind.Size() bytes.
func ReadTable(kind TableKind, r io.Reader) (*Table, error) {
	want := kind.Size()
	if want == 0 {
		return nil, checkTableSize(kind, 0)
	}
	// Read one byte more than needed so an oversized stream is detected
	// without reading all of it.
	data, err := io.ReadAll(io.LimitReader(r, int64(want)+1))
	if err != nil {
		return nil, fmt.Errorf("read lookup table: %w", err)
	}
	if len(data) > want {
		return nil, fmt.Errorf("%w: a %s table is %d bytes, got more", ErrTableSize, kind, want)
	}
	if err := checkTableSize(kind, len(data)); err != nil {
		return nil, err
	}
	return &Table{Kind: kind, Data: data}, nil
}

// LoadTableFromFile reads a table from path, taking its kind from the file
// extension (.alp, .shd or .lht).
func LoadTableFromFile(path string) (*Table, error) {
	kind, ok := TableKindFromPath(path)
	if !ok {
		return nil, fmt.Errorf("pal: %s: not a .alp, .shd or .lht lookup table", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadTable(kind, f)
}

// Rows returns the number of rows in the table.
func (t *Table) Rows() int {
	return len(t.Data) / TableColumns
}

// Row returns row r of the table (256 palette indices). It panics when r is
// out of range.
func (t *Table) Row(r int) []byte {
	return t.Data[r*TableColumns : (r+1)*TableColumns]
}

// At returns the palette index at row r, column index. For an alpha table r
// and index are the two colours being blended; for shade and light tables r
// is the level. It panics when either is out of range.
func (t *Table) At(r int, index uint8) uint8 {
	return t.Row(r)[index]
}

// Write writes the table's bytes to w.
func (t *Table) Write(w io.Writer) error {
	_, err := w.Write(t.Data)
	return err
}

// RenderSwatch draws the table as a 256-wide grid with one row per table row
// (256×256 cells for .ALP, 256×32 for .SHD and .LHT), each cell cellSize
// pixels square and filled with the palette colour its byte selects.
// cellSize<=0 defaults to 4.
func (t *Table) RenderSwatch(palette *Palette, cellSize int) (*image.RGBA, error) {
	return renderTable(t.Data, t.Rows(), palette, cellSize)
}

func renderTable(data []byte, rows int, palette *Palette, cellSize int) (*image.RGBA, error) {
	if palette == nil {
		return nil, errors.New("pal: nil palette")
	}
	if cellSize <= 0 {
		cellSize = 4
	}
	img := image.NewRGBA(image.Rect(0, 0, TableColumns*cellSize, rows*cellSize))
	for row := 0; row < rows; row++ {
		for col := 0; col < TableColumns; col++ {
			idx := data[row*TableColumns+col]
			c := color.RGBA{palette.Colors[idx].R, palette.Colors[idx].G, palette.Colors[idx].B, 255}
			for y := 0; y < cellSize; y++ {
				for x := 0; x < cellSize; x++ {
					img.SetRGBA(col*cellSize+x, row*cellSize+y, c)
				}
			}
		}
	}
	return img, nil
}

// lookupRows returns the row count of a raw table of the given length, which
// must be one of the game's table sizes.
func lookupRows(n int) (int, error) {
	switch n {
	case AlphaTableSize:
		return AlphaTableRows, nil
	case ShadeTableSize: // also LightTableSize
		return RampTableRows, nil
	default:
		return 0, fmt.Errorf("%w: lookup tables are %d (.alp) or %d (.shd, .lht) bytes, got %d",
			ErrTableSize, AlphaTableSize, ShadeTableSize, n)
	}
}

// LoadLookupFromReader reads a whole color-index lookup table (.ALP, .SHD or
// .LHT) from r and returns its bytes. The size must be one the game uses:
// 65,536 bytes for .ALP or 8,192 bytes for .SHD and .LHT.
//
// Deprecated: Use ReadTable or LoadTableFromFile, which also record the
// table's kind.
func LoadLookupFromReader(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, AlphaTableSize+1))
	if err != nil {
		return nil, fmt.Errorf("read lookup table: %w", err)
	}
	if len(data) > AlphaTableSize {
		return nil, fmt.Errorf("%w: lookup tables are at most %d bytes, got more", ErrTableSize, AlphaTableSize)
	}
	if _, err := lookupRows(len(data)); err != nil {
		return nil, err
	}
	return data, nil
}

// LoadLookupFromFile is the file-path equivalent of LoadLookupFromReader.
//
// Deprecated: Use LoadTableFromFile.
func LoadLookupFromFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return LoadLookupFromReader(f)
}

// RenderLookupSwatch renders a raw lookup table as a 256-wide grid of
// cellSize×cellSize squares, using palette for the index→RGB mapping: 256
// rows for a 65,536-byte .ALP, 32 rows for an 8,192-byte .SHD or .LHT. Any
// other size is an error. cellSize<=0 defaults to 4.
//
// Deprecated: Use Table.RenderSwatch.
func RenderLookupSwatch(table []byte, palette *Palette, cellSize int) (*image.RGBA, error) {
	rows, err := lookupRows(len(table))
	if err != nil {
		return nil, err
	}
	return renderTable(table, rows, palette, cellSize)
}
