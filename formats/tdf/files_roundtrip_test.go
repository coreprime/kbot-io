package tdf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gameTDFExtensions are the section/brace text formats handled by this codec.
// TA:Kingdoms .gui files are a different (binary token) format and are excluded.
func gameTDFExtensions(taKingdoms bool) map[string]bool {
	exts := map[string]bool{".tdf": true, ".fbi": true, ".ota": true}
	if !taKingdoms {
		exts[".gui"] = true
	}
	return exts
}

// TestCanonicalizeAllGameFiles proves every TDF/FBI/GUI/OTA file in the
// configured game directories survives parse -> emit -> parse with no semantic
// loss (comments and whitespace aside). Set TA_UNPACKED_PATH / TAK_UNPACKED_PATH
// to run it; it skips otherwise.
func TestCanonicalizeAllGameFiles(t *testing.T) {
	for _, g := range []struct {
		env      string
		kingdoms bool
	}{
		{"TA_UNPACKED_PATH", false},
		{"TAK_UNPACKED_PATH", true},
	} {
		root := os.Getenv(g.env)
		if root == "" {
			t.Logf("%s not set; skipping", g.env)
			continue
		}
		exts := gameTDFExtensions(g.kingdoms)
		t.Run(g.env, func(t *testing.T) {
			var total, skipped, failed int
			err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if !exts[strings.ToLower(filepath.Ext(path))] {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				// A handful of shipped .gui files are actually compiled/binary
				// blobs, not text TDF; skip anything with NUL bytes.
				if bytes.IndexByte(data, 0) >= 0 {
					skipped++
					return nil
				}
				total++
				canon, err := Canonicalize(data)
				if err != nil {
					failed++
					if failed <= 20 {
						t.Errorf("%s: canonicalize: %v", rel(root, path), err)
					}
					return nil
				}
				if ok, msg := SemanticEqual(data, canon); !ok {
					failed++
					if failed <= 20 {
						t.Errorf("%s: %s", rel(root, path), msg)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk: %v", err)
			}
			t.Logf("%s: %d files checked, %d binary skipped, %d failed", g.env, total, skipped, failed)
		})
	}
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}

// metaSection is a generic section that records its source with a Meta.
type metaSection struct {
	Key      string            `tdf:",name"`
	Values   map[string]string `tdf:",remaining"`
	Children []metaSection     `tdf:",sections"`
	Meta     Meta              `tdf:",meta"`
}

// TestDocumentAndMetaAllGameFiles checks, on every text file of the configured
// game directories, that the Document tree and a Meta-bearing struct both
// re-emit what the game reads (strict SemanticEqual), and that an unchanged
// Document gives its source back byte for byte.
func TestDocumentAndMetaAllGameFiles(t *testing.T) {
	for _, g := range []struct {
		env      string
		kingdoms bool
	}{
		{"TA_UNPACKED_PATH", false},
		{"TAK_UNPACKED_PATH", true},
	} {
		root := os.Getenv(g.env)
		if root == "" {
			t.Logf("%s not set; skipping", g.env)
			continue
		}
		exts := gameTDFExtensions(g.kingdoms)
		t.Run(g.env, func(t *testing.T) {
			var total, failed int
			fail := func(path, format string, args ...any) {
				failed++
				if failed <= 20 {
					t.Errorf("%s: "+format, append([]any{rel(root, path)}, args...)...)
				}
			}
			err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !exts[strings.ToLower(filepath.Ext(path))] {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil || bytes.IndexByte(data, 0) >= 0 {
					return nil
				}
				total++
				doc, err := Parse(bytes.NewReader(data))
				if err != nil {
					fail(path, "parse: %v", err)
					return nil
				}
				if same, err := doc.Bytes(); err != nil || !bytes.Equal(same, data) {
					fail(path, "unchanged Bytes differs from the source: %v", err)
				}
				var w bytes.Buffer
				if err := doc.Write(&w); err != nil {
					fail(path, "write: %v", err)
				} else if ok, msg := SemanticEqual(data, w.Bytes()); !ok {
					fail(path, "Document.Write: %s", msg)
				}
				var v metaSection
				if err := Unmarshal(data, &v); err != nil {
					fail(path, "unmarshal: %v", err)
					return nil
				}
				out, err := Marshal(&v)
				if err != nil {
					fail(path, "marshal: %v", err)
				} else if ok, msg := SemanticEqual(data, out); !ok {
					fail(path, "Meta round trip: %s", msg)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk: %v", err)
			}
			t.Logf("%s: %d files checked, %d failed", g.env, total, failed)
		})
	}
}
