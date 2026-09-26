package pal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coreprime/kbot-io/palettes"
	"github.com/coreprime/kbot-io/testutil"
)

func TestTableKindSizes(t *testing.T) {
	cases := []struct {
		kind       TableKind
		rows, size int
		ext        string
	}{
		{AlphaTable, 256, 65536, ".alp"},
		{ShadeTable, 32, 8192, ".shd"},
		{LightTable, 32, 8192, ".lht"},
	}
	for _, c := range cases {
		if c.kind.Rows() != c.rows || c.kind.Size() != c.size || c.kind.Ext() != c.ext {
			t.Errorf("%v: rows/size/ext = %d/%d/%q, want %d/%d/%q",
				c.kind, c.kind.Rows(), c.kind.Size(), c.kind.Ext(), c.rows, c.size, c.ext)
		}
		if k, ok := TableKindFromPath("palettes/PALETTE" + c.ext); !ok || k != c.kind {
			t.Errorf("TableKindFromPath(%s) = %v, %v", c.ext, k, ok)
		}
	}
	if _, ok := TableKindFromPath("palette.pal"); ok {
		t.Error("a .pal path is not a lookup table")
	}
	if TableKind(0).Size() != 0 {
		t.Error("an unknown kind should have size 0")
	}
}

func TestTableSizeValidation(t *testing.T) {
	for _, kind := range []TableKind{AlphaTable, ShadeTable, LightTable} {
		for _, n := range []int{0, 1024, kind.Size() - 1, kind.Size() + 1} {
			if _, err := NewTable(kind, make([]byte, n)); !errors.Is(err, ErrTableSize) {
				t.Errorf("%v NewTable(%d bytes): err = %v, want ErrTableSize", kind, n, err)
			}
			if _, err := ReadTable(kind, bytes.NewReader(make([]byte, n))); !errors.Is(err, ErrTableSize) {
				t.Errorf("%v ReadTable(%d bytes): err = %v, want ErrTableSize", kind, n, err)
			}
		}
		if _, err := ReadTable(kind, bytes.NewReader(make([]byte, kind.Size()))); err != nil {
			t.Errorf("%v ReadTable(exact size): %v", kind, err)
		}
	}
	if _, err := NewTable(TableKind(9), nil); err == nil {
		t.Error("expected an error for an unknown kind")
	}
}

func TestTableAccessAndSwatch(t *testing.T) {
	data := make([]byte, ShadeTableSize)
	for i := range data {
		data[i] = byte(i / TableColumns) // row r maps everything to index r
	}
	tbl, err := NewTable(ShadeTable, data)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 99 // NewTable copies
	if tbl.Rows() != 32 || tbl.At(0, 0) != 0 || tbl.At(31, 255) != 31 || len(tbl.Row(5)) != 256 {
		t.Errorf("rows=%d at(0,0)=%d at(31,255)=%d", tbl.Rows(), tbl.At(0, 0), tbl.At(31, 255))
	}
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	img, err := tbl.RenderSwatch(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 32 {
		t.Fatalf("swatch %v, want 256x32", img.Bounds())
	}
	if r, g, b, _ := img.At(10, 31).RGBA(); uint8(r>>8) != p.Colors[31].R || uint8(g>>8) != p.Colors[31].G || uint8(b>>8) != p.Colors[31].B {
		t.Error("swatch cell does not show the palette colour of its index")
	}
	if _, err := tbl.RenderSwatch(nil, 1); err == nil {
		t.Error("expected an error for a nil palette")
	}
	var buf bytes.Buffer
	if err := tbl.Write(&buf); err != nil || buf.Len() != ShadeTableSize {
		t.Errorf("Write: %d bytes, %v", buf.Len(), err)
	}
}

func TestDeprecatedLookupAPIUsesGameSizes(t *testing.T) {
	if _, err := LoadLookupFromReader(bytes.NewReader(make([]byte, 1024))); !errors.Is(err, ErrTableSize) {
		t.Errorf("1024-byte lookup: err = %v, want ErrTableSize", err)
	}
	if _, err := LoadLookupFromReader(bytes.NewReader(make([]byte, AlphaTableSize+5))); !errors.Is(err, ErrTableSize) {
		t.Errorf("oversized lookup: err = %v, want ErrTableSize", err)
	}
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	for _, c := range []struct{ size, rows int }{{AlphaTableSize, 256}, {ShadeTableSize, 32}} {
		table, err := LoadLookupFromReader(bytes.NewReader(make([]byte, c.size)))
		if err != nil {
			t.Fatalf("%d-byte lookup: %v", c.size, err)
		}
		img, err := RenderLookupSwatch(table, p, 1)
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 256 || img.Bounds().Dy() != c.rows {
			t.Errorf("%d-byte swatch %v, want 256x%d", c.size, img.Bounds(), c.rows)
		}
	}
	if _, err := RenderLookupSwatch(make([]byte, 1024), p, 1); !errors.Is(err, ErrTableSize) {
		t.Errorf("1024-byte swatch: err = %v, want ErrTableSize", err)
	}
}

func TestRetailTables(t *testing.T) {
	dir := testutil.UnpackedDir(t, "palettes")
	alp, err := LoadTableFromFile(filepath.Join(dir, "palette.alp"))
	if err != nil {
		t.Fatal(err)
	}
	for a := 0; a < 256; a++ {
		if alp.At(a, uint8(a)) != uint8(a) {
			t.Fatalf("alpha[%d][%d] = %d, want %d", a, a, alp.At(a, uint8(a)), a)
		}
		for b := 0; b < 256; b++ {
			if alp.At(a, uint8(b)) != alp.At(b, uint8(a)) {
				t.Fatalf("alpha table not symmetric at %d,%d", a, b)
			}
		}
	}
	shd, err := LoadTableFromFile(filepath.Join(dir, "palette.shd"))
	if err != nil {
		t.Fatal(err)
	}
	lht, err := LoadTableFromFile(filepath.Join(dir, "palette.lht"))
	if err != nil {
		t.Fatal(err)
	}
	if shd.Kind != ShadeTable || lht.Kind != LightTable || shd.Rows() != 32 || lht.Rows() != 32 {
		t.Fatalf("kinds/rows: %v %d, %v %d", shd.Kind, shd.Rows(), lht.Kind, lht.Rows())
	}
	p, err := LoadFromFile(filepath.Join(dir, "palette.pal"))
	if err != nil {
		t.Fatal(err)
	}
	// Shade row 0 is black; light row 0 leaves most colours unchanged.
	for i, idx := range shd.Row(0) {
		if c := p.Colors[idx]; c.R != 0 || c.G != 0 || c.B != 0 {
			t.Fatalf("shade row 0 maps %d to non-black %v", i, c)
		}
	}
	same := 0
	for i, idx := range lht.Row(0) {
		if int(idx) == i {
			same++
		}
	}
	if same < 200 {
		t.Errorf("light row 0 keeps only %d of 256 indices", same)
	}
}

func TestRetailKingdomsTables(t *testing.T) {
	dir := testutil.TAKUnpackedDir(t, "palettes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if _, ok := TableKindFromPath(e.Name()); !ok {
			continue
		}
		if _, err := LoadTableFromFile(filepath.Join(dir, e.Name())); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
		n++
	}
	if n == 0 {
		t.Error("no TA: Kingdoms lookup tables found")
	}
}
