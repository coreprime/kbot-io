package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/hpi/common"
	hpiv1 "github.com/coreprime/kbot-io/formats/hpi/v1"
	hpiv2 "github.com/coreprime/kbot-io/formats/hpi/v2"
	"github.com/coreprime/kbot-io/testutil"
)

type archiveFile struct {
	path string
	data string
}

// makeV1 writes a v1 archive at path. A nil trailer writes the retail
// default; noTrailer writes none.
func makeV1(t *testing.T, path string, noTrailer bool, files ...archiveFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := hpiv1.CreateWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if noTrailer {
		w.SetTrailer(nil)
		w.AllowNonGameTrailer = true
	}
	for _, f := range files {
		if err := w.AddFileFromBytes(f.path, []byte(f.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeV2(t *testing.T, path string, files ...archiveFile) {
	t.Helper()
	w, err := hpiv2.CreateWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := w.AddFileFromBytes(f.path, []byte(f.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, vfs *VirtualFileSystem, p string) string {
	t.Helper()
	b, err := vfs.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", p, err)
	}
	return string(b)
}

func mountNames(vfs *VirtualFileSystem) []string {
	var names []string
	for _, m := range vfs.MountOrder() {
		names = append(names, m.Name)
	}
	return names
}

func skipReasons(vfs *VirtualFileSystem) map[string]SkipReason {
	out := make(map[string]SkipReason)
	for _, s := range vfs.SkippedArchives() {
		out[s.Name] = s.Reason
	}
	return out
}

func TestGameOrderDiscovery(t *testing.T) {
	dir := t.TempDir()
	f := archiveFile{"gamedata/x.tdf", "x"}
	makeV1(t, filepath.Join(dir, "rev31.gp3"), false, f)
	makeV1(t, filepath.Join(dir, "rev30.gp3"), false, f)
	makeV1(t, filepath.Join(dir, "b.ccx"), false, f)
	makeV1(t, filepath.Join(dir, "a.CCX"), false, f)
	makeV1(t, filepath.Join(dir, "x.ufo"), false, f)
	for _, n := range []string{"h01", "h02", "h04", "h06", "h07", "h08", "h09", "h10", "h11", "h12", "h13"} {
		makeV1(t, filepath.Join(dir, n+".hpi"), false, f)
	}
	makeV1(t, filepath.Join(dir, "h03.hpi"), true, f) // no trailer: not mounted, not counted
	makeV2(t, filepath.Join(dir, "h05.hpi"), f)       // TA: Kingdoms: not mounted, not counted
	if err := os.WriteFile(filepath.Join(dir, "broken.ufo"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeV1(t, filepath.Join(dir, "sub", "nested.ufo"), false, f)

	// SkipErrors is off: game-order discovery skips what the game skips.
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatalf("NewVirtualFileSystem: %v", err)
	}
	defer func() { _ = vfs.Close() }()

	want := "rev31.gp3 a.CCX b.ccx x.ufo h01.hpi h02.hpi h04.hpi h06.hpi h07.hpi h08.hpi h09.hpi h10.hpi h11.hpi h12.hpi"
	if got := strings.Join(mountNames(vfs), " "); got != want {
		t.Errorf("mount order:\n got %s\nwant %s", got, want)
	}
	reasons := skipReasons(vfs)
	for name, reason := range map[string]SkipReason{
		"rev30.gp3":  SkipRevision,
		"h03.hpi":    SkipNoTrailer,
		"h05.hpi":    SkipVersion,
		"h13.hpi":    SkipMountLimit,
		"broken.ufo": SkipNotArchive,
		"nested.ufo": SkipSubdirectory,
	} {
		if reasons[name] != reason {
			t.Errorf("%s skipped with %q, want %q", name, reasons[name], reason)
		}
	}
	if len(reasons) != 6 {
		t.Errorf("skipped %v, want exactly six", reasons)
	}
	if got := vfs.Stats()["discovery"]; got != "game-order" {
		t.Errorf("discovery = %v", got)
	}
}

func TestGameOrderFirstMountWins(t *testing.T) {
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "rev31.gp3"), false, archiveFile{"a.txt", "gp3"})
	makeV1(t, filepath.Join(dir, "a.ccx"), false, archiveFile{"a.txt", "a.ccx"}, archiveFile{"b.txt", "a.ccx"})
	makeV1(t, filepath.Join(dir, "b.ccx"), false, archiveFile{"b.txt", "b.ccx"}, archiveFile{"c.txt", "b.ccx"})
	makeV1(t, filepath.Join(dir, "z.hpi"), false, archiveFile{"c.txt", "z.hpi"}, archiveFile{"d.txt", "z.hpi"})
	writeDisk(t, dir, "d.txt", "loose")

	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	for p, want := range map[string]string{"a.txt": "gp3", "b.txt": "a.ccx", "c.txt": "b.ccx", "d.txt": "loose"} {
		if got := mustRead(t, vfs, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	layers := vfs.GetFileLayers("c.txt")
	if len(layers) != 2 || layers[0].Source != "b.ccx" || layers[1].Source != "z.hpi" {
		t.Errorf("layers for c.txt = %+v", layers)
	}
}

func TestGameOrderSortsNamesUpperCased(t *testing.T) {
	// Upper-cased, "AB.UFO" sorts before "A_B.UFO" ('B' < '_'); lower-cased
	// the order would flip ('_' < 'b').
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "a_b.ufo"), false, archiveFile{"f.txt", "a_b"})
	makeV1(t, filepath.Join(dir, "aB.ufo"), false, archiveFile{"f.txt", "aB"})
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := mustRead(t, vfs, "f.txt"); got != "aB" {
		t.Errorf("f.txt = %q, want aB (first in upper-cased order)", got)
	}
}

func TestDiscRoots(t *testing.T) {
	dir, disc := t.TempDir(), t.TempDir()
	makeV1(t, filepath.Join(dir, "game.hpi"), false, archiveFile{"a.txt", "game"})
	makeV1(t, filepath.Join(disc, "cd.hpi"), false, archiveFile{"a.txt", "cd"}, archiveFile{"b.txt", "cd"})
	vfs, err := NewVirtualFileSystem(dir, &Config{DiscRoots: []string{disc}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := strings.Join(mountNames(vfs), " "); got != "game.hpi cd.hpi" {
		t.Errorf("mount order %q", got)
	}
	if mustRead(t, vfs, "a.txt") != "game" || mustRead(t, vfs, "b.txt") != "cd" {
		t.Error("disc root archives should rank below the game directory's")
	}
}

func TestAutoDiscoveryKeepsKingdomsOrder(t *testing.T) {
	dir := t.TempDir()
	makeV2(t, filepath.Join(dir, "data.hpi"), archiveFile{"gamedata/sidedata.tdf", "base"}, archiveFile{"only.txt", "data"})
	makeV2(t, filepath.Join(dir, "IPData.hpi"), archiveFile{"gamedata/sidedata.tdf", "iron plague"})
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := vfs.Stats()["discovery"]; got != "all-archives" {
		t.Fatalf("discovery = %v, want all-archives for a TA: Kingdoms directory", got)
	}
	if got := mustRead(t, vfs, "gamedata/sidedata.tdf"); got != "iron plague" {
		t.Errorf("sidedata = %q, want IPData.hpi to overlay data.hpi", got)
	}
	if got := mustRead(t, vfs, "only.txt"); got != "data" {
		t.Errorf("only.txt = %q", got)
	}

	// Forcing game order on the same directory mounts nothing: TA 3.1c
	// does not read version 2 archives.
	game, err := NewVirtualFileSystem(dir, &Config{Discovery: DiscoveryGameOrder})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = game.Close() }()
	if n := len(game.MountOrder()); n != 0 {
		t.Errorf("game order mounted %d TA: Kingdoms archives", n)
	}
	if reasons := skipReasons(game); reasons["data.hpi"] != SkipVersion || reasons["IPData.hpi"] != SkipVersion {
		t.Errorf("skips = %v", reasons)
	}
}

func TestAllArchivesDiscovery(t *testing.T) {
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "a.hpi"), false, archiveFile{"f.txt", "a.hpi"})
	makeV1(t, filepath.Join(dir, "z.ufo"), false, archiveFile{"f.txt", "z.ufo"})
	makeV1(t, filepath.Join(dir, "Mods", "m.ufo"), false, archiveFile{"g.txt", "mods"})
	makeV1(t, filepath.Join(dir, "Backup", "old.ufo"), false, archiveFile{"g.txt", "backup"}, archiveFile{"h.txt", "backup"})
	if err := os.WriteFile(filepath.Join(dir, "junk.hpi"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewVirtualFileSystem(dir, &Config{Discovery: DiscoveryAllArchives}); err == nil {
		t.Fatal("all-archives discovery without SkipErrors should fail on a broken archive")
	}

	vfs, err := NewVirtualFileSystem(dir, &Config{
		Discovery:          DiscoveryAllArchives,
		SkipErrors:         true,
		ExcludeDirectories: []string{"backup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := mustRead(t, vfs, "f.txt"); got != "z.ufo" {
		t.Errorf("f.txt = %q, want the last-loaded z.ufo", got)
	}
	if got := mustRead(t, vfs, "g.txt"); got != "mods" {
		t.Errorf("g.txt = %q, want the Mods archive", got)
	}
	if vfs.Exists("h.txt") {
		t.Error("archives inside an excluded directory should not be loaded")
	}
	if reasons := skipReasons(vfs); reasons["junk.hpi"] != SkipOpenFailed {
		t.Errorf("skips = %v", reasons)
	}
}

func TestConfiguredExtraExtensions(t *testing.T) {
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "pack.kbt"), false, archiveFile{"extra.txt", "extra"}, archiveFile{"shared.txt", "kbt"})
	makeV1(t, filepath.Join(dir, "pack.hpi"), false, archiveFile{"shared.txt", "hpi"})
	for _, mode := range []DiscoveryMode{DiscoveryGameOrder, DiscoveryAllArchives} {
		vfs, err := NewVirtualFileSystem(dir, &Config{Extensions: []string{".hpi", "KBT"}, Discovery: mode})
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, vfs, "extra.txt"); got != "extra" {
			t.Errorf("%v: extra.txt = %q", mode, got)
		}
		want := map[DiscoveryMode]string{DiscoveryGameOrder: "hpi", DiscoveryAllArchives: "kbt"}[mode]
		if got := mustRead(t, vfs, "shared.txt"); got != want {
			t.Errorf("%v: shared.txt = %q, want %q", mode, got, want)
		}
		if vfs.Exists("pack.kbt") {
			t.Errorf("%v: a configured archive should not appear as a loose file", mode)
		}
		_ = vfs.Close()
	}
}

func TestLookupFallsThroughArchives(t *testing.T) {
	dir := t.TempDir()
	// In a.ccx a later root file "UNITS" hides the directory "units", so
	// units/x.fbi is unreachable there and b.ccx's copy is used.
	makeV1(t, filepath.Join(dir, "a.ccx"), false,
		archiveFile{"units/x.fbi", "hidden"},
		archiveFile{"UNITS", "a file"},
	)
	makeV1(t, filepath.Join(dir, "b.ccx"), false, archiveFile{"units/x.fbi", "lower"})
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := mustRead(t, vfs, "units/x.fbi"); got != "lower" {
		t.Errorf("units/x.fbi = %q, want the lower archive's copy", got)
	}
	if layers := vfs.GetFileLayers("units/x.fbi"); len(layers) != 1 || layers[0].Source != "b.ccx" {
		t.Errorf("layers = %+v, want only b.ccx", layers)
	}
}

func TestDuplicateEntriesResolveToLast(t *testing.T) {
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "a.ufo"), false, archiveFile{"a.txt", "one"}, archiveFile{"A.TXT", "second"})
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	info, err := vfs.Stat("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, vfs, "a.txt"); got != "second" || info.Size != int64(len(got)) {
		t.Errorf("a.txt = %q with Stat size %d, want %q with a matching size", got, info.Size, "second")
	}
}

func TestBackslashAndCodePageKeys(t *testing.T) {
	dir := t.TempDir()
	makeV1(t, filepath.Join(dir, "a.ufo"), false,
		archiveFile{"units/ARMCOM.FBI", "armcom"},
		archiveFile{"maps/caf\xe9.tnt", "lower"},
		archiveFile{"maps/CAF\xc9.TNT", "upper"},
	)
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if !vfs.Exists(`units\armcom.fbi`) || mustRead(t, vfs, `UNITS\ARMCOM.FBI`) != "armcom" {
		t.Error("backslash paths should resolve on every host")
	}
	if mustRead(t, vfs, "maps/caf\xe9.tnt") != "lower" || mustRead(t, vfs, "MAPS/CAF\xc9.TNT") != "upper" {
		t.Error("names differing only in an accented code-page letter must stay distinct")
	}
}

func TestUnsafeArchiveEntriesAreNotExposed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.ufo")
	w, err := hpiv1.CreateWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	w.HeaderKey = 0 // plaintext, so the name can be patched below
	_ = w.AddFileFromBytes("QQ/escape.txt", []byte("escape"))
	_ = w.AddFileFromBytes("safe.txt", []byte("safe"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("QQ\x00"), []byte("..\x00"), 1)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	for _, key := range vfs.List() {
		if strings.Contains(key, "..") {
			t.Errorf("unsafe key %q exposed", key)
		}
	}
	if !vfs.Exists("safe.txt") {
		t.Error("safe entry missing")
	}
}

func TestListGameOrder(t *testing.T) {
	dir := t.TempDir()
	writeDisk(t, dir, "features/b.tdf", "loose b")
	writeDisk(t, dir, "features/A_x.tdf", "loose a_x")
	writeDisk(t, dir, "features/Ab.tdf", "loose ab")
	writeDisk(t, dir, "features/sub/c.tdf", "loose c")
	writeDisk(t, dir, "features/readme.txt", "not a tdf")
	makeV1(t, filepath.Join(dir, "a.ccx"), false,
		archiveFile{"features/z.tdf", "a z"},
		archiveFile{"features/b.tdf", "a b"}, // hidden by the loose file
		archiveFile{"features/deep/y.tdf", "a y"},
		archiveFile{"features/m.tdf", "a m"},
	)
	makeV1(t, filepath.Join(dir, "b.ccx"), false,
		archiveFile{"features/z.tdf", "b z"}, // duplicate of a.ccx's, kept
		archiveFile{"FEATURES/n.TDF", "b n"}, // directory merged into "features"
	)
	vfs, err := NewVirtualFileSystem(dir, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()

	var got []string
	for _, e := range vfs.ListGameOrder(`FEATURES\`, ".TDF", true) {
		got = append(got, e.Source+":"+e.StoredPath+":"+map[bool]string{true: "active", false: "shadowed"}[e.Active])
	}
	want := []string{
		"Physical Filesystem:features/Ab.tdf:active",
		"Physical Filesystem:features/A_x.tdf:active",
		"Physical Filesystem:features/b.tdf:active",
		"Physical Filesystem:features/sub/c.tdf:active",
		"a.ccx:features/z.tdf:active",
		"a.ccx:features/deep/y.tdf:active",
		"a.ccx:features/m.tdf:active",
		"b.ccx:features/z.tdf:shadowed",
		"b.ccx:features/n.TDF:active", // merged into the first spelling
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("recursive listing:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}

	flat := vfs.ListGameOrder("features", ".tdf", false)
	for _, e := range flat {
		if strings.Count(e.Path, "/") != 1 {
			t.Errorf("non-recursive listing returned %s", e.Path)
		}
	}
	if len(flat) != 7 {
		t.Errorf("non-recursive listing has %d entries, want 7", len(flat))
	}
	if n := len(vfs.ListGameOrder("features", "", false)); n != 8 {
		t.Errorf("listing with no extension filter has %d entries, want 8", n)
	}
}

func TestWritableOverlayRejectsTraversal(t *testing.T) {
	vfs, base, work := newOverlay(t)
	writeDisk(t, base, "units/armcom.fbi", "base")
	for _, p := range []string{"../escape.txt", `..\escape.txt`, "units/../../escape.txt", "a//b", "./a"} {
		if err := vfs.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) was accepted", p)
		}
		if err := vfs.EnsureLocal(p); err == nil {
			t.Errorf("EnsureLocal(%q) was accepted", p)
		}
		if err := vfs.Remove(p); err == nil {
			t.Errorf("Remove(%q) was accepted", p)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "escape.txt")); err == nil {
		t.Fatal("a write escaped the work folder")
	}
	// Backslash paths land in the right place.
	if err := vfs.WriteFile(`units\new.fbi`, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "units", "new.fbi")); err != nil {
		t.Errorf("backslash write did not create units/new.fbi: %v", err)
	}
	entries := vfs.ListGameOrder("units", ".fbi", false)
	if len(entries) != 1 || entries[0].Source != "Workspace" || !entries[0].Active {
		t.Errorf("game-order listing after write = %+v", entries)
	}
	if err := vfs.Remove("units/new.fbi"); err != nil {
		t.Fatal(err)
	}
	if n := len(vfs.ListGameOrder("units", ".fbi", false)); n != 0 {
		t.Errorf("removed file still listed (%d entries)", n)
	}
}

// TestGameOrderRetailInstall checks game-order discovery against the retail
// Total Annihilation install.
func TestGameOrderRetailInstall(t *testing.T) {
	root := testutil.PackedPath(t)
	vfs, err := NewVirtualFileSystem(root, &Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	mounts := mountNames(vfs)
	if len(mounts) == 0 || mounts[0] != "rev31.gp3" {
		t.Fatalf("mount order starts %v, want rev31.gp3 first", mounts)
	}
	reasons := skipReasons(vfs)
	for _, n := range []string{"totala3.hpi", "totala4.hpi", "worlds.hpi"} {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil && reasons[n] != SkipMountLimit {
			t.Errorf("%s: skip reason %q, want %q", n, reasons[n], SkipMountLimit)
		}
	}
	info, err := vfs.Stat("anims/armhp1.gaf")
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "btdata.ccx" {
		t.Errorf("anims/armhp1.gaf comes from %s, want btdata.ccx", info.Source)
	}
}

// TestAutoDiscoveryRetailKingdoms checks that a TA: Kingdoms install keeps
// its archive overlay order.
func TestAutoDiscoveryRetailKingdoms(t *testing.T) {
	root := os.Getenv("TAK_PACKED_PATH")
	if root == "" {
		t.Skip("TAK_PACKED_PATH not set")
	}
	if _, err := os.Stat(filepath.Join(root, "IPData.hpi")); err != nil {
		t.Skipf("IPData.hpi not available: %v", err)
	}
	vfs, err := NewVirtualFileSystem(root, &Config{SkipErrors: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vfs.Close() }()
	if got := vfs.Stats()["discovery"]; got != "all-archives" {
		t.Errorf("discovery = %v, want all-archives", got)
	}
	info, err := vfs.Stat("gamedata/sidedata.tdf")
	if err != nil {
		t.Fatal(err)
	}
	if !common.EqualFoldASCII(info.Source, "IPData.hpi") {
		t.Errorf("sidedata.tdf comes from %s, want IPData.hpi", info.Source)
	}
}
