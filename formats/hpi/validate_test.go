package hpi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coreprime/kbot-io/formats/hpi/common"
	"github.com/coreprime/kbot-io/formats/hpi/v1"
	"github.com/coreprime/kbot-io/formats/hpi/v2"
)

func writeV1(t *testing.T, key uint8, trailer []byte, allowBad bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.hpi")
	w, err := v1.CreateWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	w.HeaderKey = key
	w.SetTrailer(trailer)
	w.AllowNonGameTrailer = allowBad
	if err := w.AddFileFromBytes("units/x.fbi", []byte("[UNITINFO]{}")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateMountableArchive(t *testing.T) {
	v, err := Validate(writeV1(t, DefaultHeaderKey, []byte("Copyright 1998 Cavedog Entertainment"), false))
	if err != nil {
		t.Fatal(err)
	}
	if !v.GameMountable || v.Problem != MountOK {
		t.Fatalf("GameMountable=%v Problem=%v %s", v.GameMountable, v.Problem, v.Detail)
	}
	if !v.MarkerValid || v.Version != VersionV1 || !v.TrailerValid || v.TrailerYear != "1998" {
		t.Errorf("unexpected validation %+v", v)
	}
	if v.HeaderKey != 0xBF || v.EffectiveKey != 0xFE || !v.Encrypted || v.FileCount != 1 {
		t.Errorf("key or count wrong: %+v", v)
	}
}

func TestValidatePlaintextKeys(t *testing.T) {
	for _, key := range []uint8{0, 0xFF} {
		v, err := Validate(writeV1(t, key, []byte(DefaultTrailer), false))
		if err != nil {
			t.Fatal(err)
		}
		if v.EffectiveKey != 0 || v.Encrypted || !v.GameMountable {
			t.Errorf("key 0x%02X: %+v", key, v)
		}
	}
}

func TestValidateMissingTrailer(t *testing.T) {
	a, v, err := OpenForGame(writeV1(t, DefaultHeaderKey, nil, true))
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		_ = a.Close()
		t.Error("OpenForGame returned an archive the game would refuse")
	}
	if v.GameMountable || v.Problem != MountNoTrailer || v.TrailerValid {
		t.Errorf("trailerless archive: %+v", v)
	}
}

func TestValidateKingdomsArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.hpi")
	w, err := v2.CreateWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.AddFileFromBytes("a.txt", []byte("a"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	v, err := Validate(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.GameMountable || v.Problem != MountBadVersion || v.Version != VersionV2 {
		t.Errorf("v2 archive: %+v", v)
	}
}

func TestValidateNotAnArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junk.hpi")
	if err := os.WriteFile(p, []byte("not an archive at all, just text"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Validate(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.GameMountable || v.Problem != MountBadMarker {
		t.Errorf("junk file: %+v", v)
	}
	if _, err := Validate(filepath.Join(t.TempDir(), "missing.hpi")); err == nil {
		t.Error("a missing file should be an error")
	}
}

func TestValidateBadDirectory(t *testing.T) {
	p := writeV1(t, 0, []byte(DefaultTrailer), false)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Point the root offset into the header.
	raw[16] = 4
	raw[17], raw[18], raw[19] = 0, 0, 0
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Validate(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.GameMountable || v.Problem != MountBadDirectory || v.Detail == "" {
		t.Errorf("bad directory: %+v", v)
	}
	if common.ValidTrailer(v.Tail) != v.TrailerValid {
		t.Error("Tail and TrailerValid disagree")
	}
}
