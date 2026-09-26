package objects3d

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func TestLoad3DO(t *testing.T) {
	path := testutil.UnpackedFile(t, "objects3d", "corvp.3do")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	t.Logf("Root: %s, Objects: %d, Vertices: %d, Primitives: %d, Textures: %d",
		m.Root.Name, len(m.AllObjects), m.TotalVertices(), m.TotalPrimitives(), len(m.Textures()))

	for _, o := range m.AllObjects {
		t.Logf("  %s: %d verts, %d prims", o.Name, len(o.Vertices), len(o.Primitives))
	}
}

func TestLoadAll3DO(t *testing.T) {
	loadCorpus(t, testutil.UnpackedDir(t, "objects3d"))
}

func TestLoadAll3DOKingdoms(t *testing.T) {
	loadCorpus(t, testutil.TAKUnpackedDir(t, "objects3d"))
}

// loadCorpus loads every .3do in dir and fails on any load error.
func loadCorpus(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	total, objects, siblings, vertices, primitives := 0, 0, 0, 0, 0
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".3do") {
			continue
		}
		total++
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		m, err := LoadFromReader(bytes.NewReader(data))
		if err != nil {
			t.Errorf("FAIL %s: %v", e.Name(), err)
			continue
		}
		objects += len(m.AllObjects)
		siblings += len(m.RootSiblings)
		vertices += m.TotalVertices()
		primitives += m.TotalPrimitives()
	}
	if total == 0 {
		t.Fatalf("no .3do files in %s", root)
	}
	t.Logf("3DO corpus: %d files, %d objects (%d root siblings), %d vertices, %d primitives",
		total, objects, siblings, vertices, primitives)
}
