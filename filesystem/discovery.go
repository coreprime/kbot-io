package filesystem

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coreprime/kbot-io/formats/hpi"
	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// standardArchiveExts are the archive extensions the game knows, in the order
// all-archives discovery loads them (lowest precedence first).
var standardArchiveExts = []string{".hpi", ".ccx", ".gp3", ".ufo"}

// defaultGameVersion names the revision archive TA 3.1c mounts: rev31.gp3.
const defaultGameVersion = "31"

// gameHPILimit is how many *.hpi archives from the game directory the game
// mounts. Archives that fail to open do not count.
const gameHPILimit = 10

// SkipReason says why a discovered archive was not mounted.
type SkipReason string

const (
	// SkipExcluded: the archive matches the Config's exclusions.
	SkipExcluded SkipReason = "excluded"
	// SkipNotArchive: the file is not an HPI archive (bad marker, too short).
	SkipNotArchive SkipReason = "not-an-archive"
	// SkipVersion: the archive is not version 1. TA 3.1c does not mount TA:
	// Kingdoms (version 2) archives.
	SkipVersion SkipReason = "version"
	// SkipNoTrailer: the archive does not end with the Cavedog copyright
	// trailer, so TA 3.1c does not mount it.
	SkipNoTrailer SkipReason = "no-trailer"
	// SkipBadDirectory: the archive's directory could not be read.
	SkipBadDirectory SkipReason = "bad-directory"
	// SkipOpenFailed: the file or its directory could not be read.
	SkipOpenFailed SkipReason = "open-failed"
	// SkipMountLimit: the game had already mounted ten *.hpi archives from
	// the game directory.
	SkipMountLimit SkipReason = "mount-limit"
	// SkipRevision: a .gp3 archive other than rev<GameVersion>.gp3, which the
	// game never looks for.
	SkipRevision SkipReason = "revision"
	// SkipSubdirectory: an archive below the top level of the game directory,
	// which game-order discovery does not scan.
	SkipSubdirectory SkipReason = "subdirectory"
	// SkipAlreadyMounted: the same file was already mounted.
	SkipAlreadyMounted SkipReason = "already-mounted"
)

// SkippedArchive is an archive file discovery found but did not mount.
type SkippedArchive struct {
	Name   string     // file name
	Path   string     // path on disk
	Reason SkipReason // why it was not mounted
	Detail string     // human-readable explanation
}

// MountedArchive is an archive mounted in the VFS.
type MountedArchive struct {
	Name    string // file name, also the FileLayer.Source label
	Path    string // path on disk
	Version uint32 // hpi.VersionV1 or hpi.VersionV2
}

// MountOrder returns the mounted archives in lookup order: a path held by
// several archives resolves to the first of them in this list. Within one
// context directory its loose files come before all of its archives; across
// the sources of NewLayered, a higher source's archives come before a lower
// source's.
func (vfs *VirtualFileSystem) MountOrder() []MountedArchive {
	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()
	var out []MountedArchive
	for _, rec := range vfs.layersByPrecedence() {
		if rec.archive == nil {
			continue
		}
		out = append(out, MountedArchive{
			Name:    rec.archive.name,
			Path:    rec.archive.archivePath,
			Version: rec.archive.reader.Version(),
		})
	}
	return out
}

// SkippedArchives returns the archive files discovery found but did not
// mount, with the reason for each, in the order they were considered.
func (vfs *VirtualFileSystem) SkippedArchives() []SkippedArchive {
	return append([]SkippedArchive(nil), vfs.skipped...)
}

// layersByPrecedence returns every layer, highest precedence first. Callers
// must hold filesMu.
func (vfs *VirtualFileSystem) layersByPrecedence() []*layerRecord {
	out := append([]*layerRecord(nil), vfs.layers...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}

func (vfs *VirtualFileSystem) skip(name, path string, reason SkipReason, detail string) {
	vfs.skipped = append(vfs.skipped, SkippedArchive{Name: name, Path: path, Reason: reason, Detail: detail})
}

// archiveExts returns the configured archive extensions, lower-cased with a
// leading dot, without duplicates: the standard ones in their fixed order,
// then any others in the order configured.
func (vfs *VirtualFileSystem) archiveExts() []string {
	configured := make(map[string]bool)
	var extras []string
	for _, ext := range vfs.config.Extensions {
		ext = common.ToLowerASCII(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if configured[ext] {
			continue
		}
		configured[ext] = true
		if !isStandardExt(ext) {
			extras = append(extras, ext)
		}
	}
	var out []string
	for _, ext := range standardArchiveExts {
		if configured[ext] {
			out = append(out, ext)
		}
	}
	return append(out, extras...)
}

func (vfs *VirtualFileSystem) archiveExtSet() map[string]bool {
	set := make(map[string]bool)
	for _, ext := range vfs.archiveExts() {
		set[ext] = true
	}
	return set
}

func isStandardExt(ext string) bool {
	for _, s := range standardArchiveExts {
		if s == ext {
			return true
		}
	}
	return false
}

// loadContextDir loads a game directory: its archives with the configured
// discovery mode, then its loose files on top.
func (vfs *VirtualFileSystem) loadContextDir(dir string) error {
	mode := vfs.resolveDiscovery(dir)
	vfs.discovery = append(vfs.discovery, mode)

	var archiveErr error
	if mode == DiscoveryGameOrder {
		archiveErr = vfs.loadGameOrder(dir)
	} else {
		archiveErr = vfs.loadArchivesFrom(dir)
	}
	if archiveErr != nil && !vfs.config.SkipErrors {
		return archiveErr
	}
	_, err := vfs.scanLooseFrom(dir, physicalSource, mode == DiscoveryGameOrder)
	return err
}

// resolveDiscovery returns the mode for a directory, deciding DiscoveryAuto
// from the versions of its top-level archives.
func (vfs *VirtualFileSystem) resolveDiscovery(dir string) DiscoveryMode {
	if vfs.config.Discovery != DiscoveryAuto {
		return vfs.config.Discovery
	}
	exts := vfs.archiveExtSet()
	v1, v2 := 0, 0
	for _, f := range topLevelFiles(dir) {
		if !exts[common.ToLowerASCII(filepath.Ext(f))] {
			continue
		}
		switch peekVersion(filepath.Join(dir, f)) {
		case hpi.VersionV1:
			v1++
		case hpi.VersionV2:
			v2++
		}
	}
	if v2 > 0 && v1 == 0 {
		return DiscoveryAllArchives
	}
	return DiscoveryGameOrder
}

// peekVersion returns the version word of an HPI file, or 0.
func peekVersion(path string) uint32 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	var head [8]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return 0
	}
	if binary.LittleEndian.Uint32(head[:4]) != common.HeaderMarker {
		return 0
	}
	return binary.LittleEndian.Uint32(head[4:])
}

// topLevelFiles returns the names of the regular files directly inside dir
// (following symbolic links), in the order the game enumerates them: by name
// with ASCII letters upper-cased, compared byte by byte.
func topLevelFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(dir, e.Name()))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
		} else if !e.Type().IsRegular() {
			continue
		}
		names = append(names, e.Name())
	}
	sortGameOrder(names)
	return names
}

// sortGameOrder sorts names the way the game's directory enumeration returns
// them: compared byte by byte after upper-casing ASCII letters.
func sortGameOrder(names []string) {
	sort.SliceStable(names, func(i, j int) bool {
		a, b := common.ToUpperASCII(names[i]), common.ToUpperASCII(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
}

// loadGameOrder mounts dir's archives the way TA 3.1c does. Archives the game
// would not mount are reported and skipped; they never abort the load.
func (vfs *VirtualFileSystem) loadGameOrder(dir string) error {
	if _, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("failed to scan for archives: %w", err)
	}
	exts := vfs.archiveExtSet()
	version := vfs.config.GameVersion
	if version == "" {
		version = defaultGameVersion
	}
	revision := "rev" + version + ".gp3"

	var plan []*archiveLayer
	mounted := make(map[string]bool)

	// try opens one candidate. It returns true when the game would mount it,
	// including when the Config excludes it (the game still mounts it, so it
	// still counts toward the *.hpi limit).
	try := func(name, path string) bool {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if mounted[abs] {
			vfs.skip(name, path, SkipAlreadyMounted, "the same file is already mounted")
			return false
		}
		a, v, err := hpi.OpenForGame(path)
		if err != nil {
			vfs.skip(name, path, SkipOpenFailed, err.Error())
			return false
		}
		if a == nil {
			vfs.skip(name, path, skipReasonFor(v.Problem), describeProblem(v))
			return false
		}
		mounted[abs] = true
		if vfs.ShouldExclude(name, false) {
			_ = a.Close()
			vfs.skip(name, path, SkipExcluded, "excluded by the configuration")
			return true
		}
		plan = append(plan, &archiveLayer{name: name, reader: a, archivePath: path})
		return true
	}

	names := topLevelFiles(dir)
	byExt := func(ext string) []string {
		var out []string
		for _, n := range names {
			if common.ToLowerASCII(filepath.Ext(n)) == ext {
				out = append(out, n)
			}
		}
		return out
	}

	if exts[".gp3"] {
		for _, n := range byExt(".gp3") {
			if !common.EqualFoldASCII(n, revision) {
				vfs.skip(n, filepath.Join(dir, n), SkipRevision, "the game mounts only "+revision)
				continue
			}
			try(n, filepath.Join(dir, n))
		}
	}
	for _, ext := range []string{".ccx", ".ufo"} {
		if !exts[ext] {
			continue
		}
		for _, n := range byExt(ext) {
			try(n, filepath.Join(dir, n))
		}
	}
	if exts[".hpi"] {
		count := 0
		for _, n := range byExt(".hpi") {
			p := filepath.Join(dir, n)
			if count == gameHPILimit {
				vfs.skip(n, p, SkipMountLimit, fmt.Sprintf("the game mounts only the first %d *.hpi archives that open", gameHPILimit))
				continue
			}
			if try(n, p) {
				count++
			}
		}
		for _, root := range vfs.config.DiscRoots {
			for _, n := range topLevelFiles(root) {
				if common.ToLowerASCII(filepath.Ext(n)) == ".hpi" {
					try(n, filepath.Join(root, n))
				}
			}
		}
	}
	for _, ext := range vfs.archiveExts() {
		if isStandardExt(ext) {
			continue
		}
		for _, n := range byExt(ext) {
			try(n, filepath.Join(dir, n))
		}
	}

	// Load the lowest-precedence archive first so that the first archive the
	// game mounts ends up on top.
	for i := len(plan) - 1; i >= 0; i-- {
		vfs.mountArchive(plan[i])
	}
	return nil
}

func skipReasonFor(p hpi.MountProblem) SkipReason {
	switch p {
	case hpi.MountBadMarker:
		return SkipNotArchive
	case hpi.MountBadVersion:
		return SkipVersion
	case hpi.MountNoTrailer:
		return SkipNoTrailer
	case hpi.MountBadDirectory:
		return SkipBadDirectory
	default:
		return SkipOpenFailed
	}
}

func describeProblem(v *hpi.Validation) string {
	if v.Detail == "" {
		return v.Problem.String()
	}
	return v.Problem.String() + ": " + v.Detail
}

// loadArchivesFrom loads every archive found under basePath (all-archives
// discovery): grouped by extension in the order .hpi, .ccx, .gp3, .ufo, then
// any other configured extension, each group sorted by lower-cased name. A
// later archive overrides an earlier one. Excluded directories are not
// searched and unreadable subdirectories are skipped.
func (vfs *VirtualFileSystem) loadArchivesFrom(basePath string) error {
	archivesByExt := make(map[string][]string) // extension -> []paths
	exts := vfs.archiveExtSet()

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == basePath {
				return err
			}
			vfs.skip(filepath.Base(path), path, SkipOpenFailed, "cannot read: "+err.Error())
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(basePath, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			if rel != "." && vfs.ShouldExclude(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := common.ToLowerASCII(filepath.Ext(path))
		if !exts[ext] {
			return nil
		}
		if vfs.ShouldExclude(rel, false) {
			vfs.skip(filepath.Base(path), path, SkipExcluded, "excluded by the configuration")
			return nil
		}
		archivesByExt[ext] = append(archivesByExt[ext], path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to scan for archives: %w", err)
	}

	for _, ext := range vfs.archiveExts() {
		paths := archivesByExt[ext]

		// Sort files within the same extension by case-insensitive name, so the
		// load order (and thus override precedence — later archives win) is
		// stable regardless of letter case. TA:Kingdoms relies on this: data.hpi
		// must be overlaid by IPData.hpi (case-sensitive byte order would put
		// the upper-case "IPData" first, letting data.hpi clobber the Iron
		// Plague overrides — e.g. the Creon side in gamedata/sidedata.tdf).
		sort.SliceStable(paths, func(i, j int) bool {
			ni, nj := common.ToLowerASCII(filepath.Base(paths[i])), common.ToLowerASCII(filepath.Base(paths[j]))
			if ni != nj {
				return ni < nj
			}
			return paths[i] < paths[j]
		})

		for _, path := range paths {
			name := filepath.Base(path)
			if err := vfs.loadArchive(name, path); err != nil {
				if vfs.config.SkipErrors {
					vfs.skip(name, path, SkipOpenFailed, err.Error())
					continue
				}
				return fmt.Errorf("failed to load archive %s: %w", name, err)
			}
		}
	}

	return nil
}
