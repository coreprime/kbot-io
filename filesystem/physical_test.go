package filesystem

import (
	"path/filepath"
	"testing"
)

func TestPhysicalFileSystemStaysInBase(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	writeDisk(t, base, "scripts/a.h", "a")
	writeDisk(t, root, "secret.txt", "secret")
	pfs, err := NewPhysicalFileSystem(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../secret.txt", `..\secret.txt`, "scripts/../../secret.txt"} {
		if _, err := pfs.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) escaped the base", p)
		}
		if pfs.Exists(p) {
			t.Errorf("Exists(%q) = true", p)
		}
		if _, err := pfs.Open(p); err == nil {
			t.Errorf("Open(%q) escaped the base", p)
		}
	}
	for _, p := range []string{"scripts/a.h", `scripts\a.h`, "scripts/../scripts/a.h"} {
		if b, err := pfs.ReadFile(p); err != nil || string(b) != "a" {
			t.Errorf("ReadFile(%q) = %q, %v", p, b, err)
		}
	}
}
