// Package filesystem provides a virtual filesystem that layers multiple archive formats
// (HPI, UFO, CCX, GP3) with physical files in a directory.
//
// The VFS presents a unified view of files from multiple sources:
// - Archive files (.hpi, .ufo, .ccx, .gp3)
// - Physical files on disk
//
// Physical (loose) files in a game directory always override that
// directory's archive files. Between archives, precedence depends on the
// discovery mode (see Config.Discovery):
//
//   - DiscoveryGameOrder reproduces Total Annihilation 3.1c. Only the top
//     level of the game directory is scanned. The game mounts rev31.gp3 (the
//     revision is Config.GameVersion), then every *.ccx, then every *.ufo, then
//     the first ten *.hpi that open, each group sorted by name with ASCII
//     letters upper-cased, then the *.hpi files of any Config.DiscRoots. An
//     archive mounts only if it is a version 1 archive whose last 36 bytes are
//     the Cavedog copyright trailer and whose directory reads; any other file
//     is skipped, reported by SkippedArchives, and does not count toward the
//     ten-archive limit. A path resolves to the first mounted archive that
//     holds it as a file.
//   - DiscoveryAllArchives walks the whole directory tree and loads every
//     archive with a configured extension in the order .hpi, .ccx, .gp3, .ufo
//     (then any other configured extension), each group sorted by
//     lower-cased name; a later archive overrides an earlier one. TA: Kingdoms
//     needs this order: data.hpi must be overlaid by IPData.hpi.
//   - DiscoveryAuto, the default, uses DiscoveryAllArchives for a directory
//     whose top-level archives are all TA: Kingdoms (version 2) archives and
//     DiscoveryGameOrder otherwise.
//
// MountOrder lists the mounted archives in lookup order, and ListGameOrder
// enumerates a directory the way the game's loaders see it.
//
// Lookups follow the game's rules. Paths may use '\' or '/' on every host,
// and names compare with ASCII letter case folded (bytes 0x80 and above,
// such as accented code-page letters, compare exactly). Within an archive the
// last of several same-named entries wins, and a path that stops on a
// directory or passes through a file falls through to the next archive that
// holds it as a file. Archive entries whose path has an empty, "." or ".."
// segment, or whose name contains a separator, are not exposed: no lookup can
// reach them.
//
// Loose files whose paths differ only in letter case (possible on
// case-sensitive hosts, never on Windows, so never in the game) share one key;
// the spelling walked last in byte order wins and the others stay as lower
// layers.
//
// For multi-source layering (a base game, optional parent contexts and a writable
// work folder overlaid on top) see NewLayered in layered.go.
//
// Example usage:
//
//	config := &filesystem.Config{
//	    Extensions: []string{".hpi", ".ccx", ".gp3", ".ufo"},
//	}
//	vfs, err := filesystem.NewVirtualFileSystem("/path/to/game", config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer func() { _ = vfs.Close() }()
//
//	// List all files
//	files := vfs.List()
//
//	// Open a file
//	reader, err := vfs.Open("units/ARMCOM.FBI")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer func() { _ = reader.Close() }()
//
//	// Walk the filesystem
//	_ = vfs.Walk(func(path string, info FileInfo) error {
//	    fmt.Printf("%s (%d bytes)\n", path, info.Size)
//	    return nil
//	})
package filesystem

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/coreprime/kbot-io/formats/hpi"
	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// Config configures the virtual filesystem
type Config struct {
	// Extensions specifies which archive extensions to load
	// Example: []string{".hpi", ".ufo", ".ccx", ".gp3"}
	// If empty, defaults to all supported formats. Files with a listed
	// extension are treated as archives and never appear as loose files.
	// Extensions beyond the four standard ones are loaded after them: in
	// game-order discovery they are mounted after the game's own groups
	// (lowest precedence), in all-archives discovery they load last (highest
	// precedence), each group in name order.
	Extensions []string

	// ExcludeDirectories is a list of directory names to ignore (case-insensitive)
	// Example: []string{"Docs", "Backup"}
	ExcludeDirectories []string

	// ExcludeExtensions is a list of file extensions to ignore (case-insensitive)
	// Example: []string{".dll", ".exe", ".ico"}
	ExcludeExtensions []string

	// ExcludePrefixes is a list of filename prefixes to ignore (case-insensitive)
	// Example: []string{"goggame", "temp_"}
	ExcludePrefixes []string

	// CaseSensitive controls path matching (default: false for TA compatibility)
	CaseSensitive bool

	// SkipErrors continues loading even if some archives fail. Game-order
	// discovery always skips archives the game would not mount; with
	// all-archives discovery a failing archive aborts the load unless
	// SkipErrors is set. Skipped archives are listed by SkippedArchives.
	SkipErrors bool

	// Discovery selects how a context directory's archives are found and
	// ordered. The zero value is DiscoveryAuto.
	Discovery DiscoveryMode

	// GameVersion names the revision archive game-order discovery mounts
	// first: rev<GameVersion>.gp3. Empty means "31" (TA 3.1c).
	GameVersion string

	// DiscRoots lists directories scanned after the game directory in
	// game-order discovery, like the game's scan of its CD: the *.hpi files
	// at the top level of each, in name order, with no count limit.
	DiscRoots []string
}

// DiscoveryMode selects how a context directory's archives are discovered.
type DiscoveryMode int

const (
	// DiscoveryAuto uses DiscoveryAllArchives when every archive at the top
	// level of the directory is a TA: Kingdoms (version 2) archive, and
	// DiscoveryGameOrder otherwise.
	DiscoveryAuto DiscoveryMode = iota
	// DiscoveryGameOrder mounts archives the way Total Annihilation 3.1c does
	// (see the package documentation). The first mounted archive holding a
	// path wins.
	DiscoveryGameOrder
	// DiscoveryAllArchives loads every archive under the directory tree in
	// extension then name order; the last one loaded holding a path wins.
	DiscoveryAllArchives
)

// String returns "auto", "game-order" or "all-archives".
func (m DiscoveryMode) String() string {
	switch m {
	case DiscoveryAuto:
		return "auto"
	case DiscoveryGameOrder:
		return "game-order"
	case DiscoveryAllArchives:
		return "all-archives"
	default:
		return fmt.Sprintf("DiscoveryMode(%d)", int(m))
	}
}

// physicalSource is the layer label used for loose files found inside a context
// directory (as opposed to the writable work-folder overlay, which carries its
// own label).
const physicalSource = "Physical Filesystem"

// FileInfo provides information about a virtual file
type FileInfo struct {
	Path   string // Virtual path (e.g., "units/ARMCOM.FBI")
	Size   int64  // File size in bytes
	Source string // Source (archive name or "disk")
	IsDir  bool   // True if this is a directory
}

// Ensure VirtualFileSystem implements the filesystem interfaces
var (
	_ FileSystem         = (*VirtualFileSystem)(nil)
	_ WritableFileSystem = (*VirtualFileSystem)(nil)
)

// VirtualFileSystem provides a layered view of archive and physical files.
//
// Files are resolved through an ordered stack of sources (see NewLayered). Each
// file carries one FileLayer per source that contains it; the layer with the
// highest load sequence (the top-most source) is the active version.
type VirtualFileSystem struct {
	basePath string
	config   *Config
	archives []*archiveLayer

	// filesMu guards files, fileLayers, directories, physicalFiles, layers and
	// seqCounter so background MD5 hashing and write operations don't race.
	filesMu       sync.RWMutex
	files         map[string]*virtualFile // Map of normalized path -> active file
	fileLayers    map[string][]FileLayer  // Map of path -> all layers containing it
	directories   map[string]bool         // Set of directory paths
	physicalFiles map[string]string       // Map of normalized path -> active physical path
	seqCounter    int                     // Monotonic layer load sequence
	layers        []*layerRecord          // Every loaded layer, in load order

	// skipped lists archives that were found but not mounted.
	skipped []SkippedArchive
	// discovery records the mode each context directory was loaded with.
	discovery []DiscoveryMode

	// writeDir is the physical directory backing the writable overlay layer.
	// It is empty for a read-only VFS (e.g. a bare context browsing tab).
	writeDir      string
	writableLabel string       // FileLayer.Source label for the writable overlay
	writableLayer *layerRecord // the writable overlay's layer, if any

	md5Hashes map[string]string // Map of normalized path -> MD5 hex hash
	md5Mutex  sync.RWMutex      // Protects md5Hashes

	// Metrics callback for tracking I/O
	metricsCallback func(bytes int64)
	metricsMutex    sync.RWMutex
}

// archiveLayer represents a loaded archive
type archiveLayer struct {
	name        string
	reader      hpi.Archive
	archivePath string
	seq         int
	mu          sync.Mutex // Protects reader access
}

// layerRecord is one loaded layer: an archive, or a directory of loose files.
type layerRecord struct {
	seq     int
	label   string
	archive *archiveLayer // nil for loose layers
	loose   []looseFile   // loose layers only
}

// looseFile is one file of a loose layer.
type looseFile struct {
	key      string // VFS key
	stored   string // path relative to the layer root, '/'-separated, as on disk
	physPath string
	size     int64
}

// virtualFile represents the active version of a file in the VFS
type virtualFile struct {
	path    string // Normalized path
	size    int64
	source  string // Archive name or "disk"
	archive *archiveLayer
	entry   *hpi.Entry
}

// NewVirtualFileSystem creates a read-only virtual filesystem over a single
// directory (its archives plus loose physical files). It is a thin wrapper over
// NewLayered for backward compatibility.
func NewVirtualFileSystem(basePath string, config *Config) (*VirtualFileSystem, error) {
	return NewLayered([]Source{{Kind: SourceContextDir, Path: basePath}}, config)
}

// newVFS allocates an empty VFS with the given config and initialised maps.
func newVFS(config *Config) *VirtualFileSystem {
	return &VirtualFileSystem{
		config:        config,
		fileLayers:    make(map[string][]FileLayer),
		archives:      make([]*archiveLayer, 0),
		files:         make(map[string]*virtualFile),
		directories:   make(map[string]bool),
		physicalFiles: make(map[string]string),
		md5Hashes:     make(map[string]string),
	}
}

// Close closes all open archives
func (vfs *VirtualFileSystem) Close() error {
	var lastErr error
	for _, archive := range vfs.archives {
		if err := archive.reader.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// nextSeq returns the next monotonic load sequence. Callers must hold filesMu
// when invoking this concurrently with writes; it is unlocked during initial
// load, which is single-threaded.
func (vfs *VirtualFileSystem) nextSeq() int {
	vfs.seqCounter++
	return vfs.seqCounter
}

// addDirs registers all parent directories of a normalized path.
func (vfs *VirtualFileSystem) addDirs(normalized string) {
	dir := path.Dir(normalized)
	for dir != "." && dir != "/" && dir != "" {
		vfs.directories[dir] = true
		dir = path.Dir(dir)
	}
}

// applyLayer records a layer for a path and makes it the active version. It is
// used during the initial (single-threaded) load, where layers are applied in
// ascending sequence so the last one applied is always the highest priority.
func (vfs *VirtualFileSystem) applyLayer(normalized string, layer FileLayer) {
	vfs.fileLayers[normalized] = append(vfs.fileLayers[normalized], layer)
	vfs.setActiveFromLayer(normalized, layer)
}

// setActiveFromLayer points the active-file maps at the given layer.
func (vfs *VirtualFileSystem) setActiveFromLayer(normalized string, layer FileLayer) {
	if layer.archive != nil {
		vfs.files[normalized] = &virtualFile{
			path:    normalized,
			size:    layer.Size,
			source:  layer.Source,
			archive: layer.archive,
			entry:   layer.entry,
		}
		delete(vfs.physicalFiles, normalized)
		return
	}
	vfs.files[normalized] = &virtualFile{
		path:   normalized,
		size:   layer.Size,
		source: "disk",
	}
	vfs.physicalFiles[normalized] = layer.physPath
}

// loadArchive opens a single archive of any version and adds its files to the
// VFS as one layer.
func (vfs *VirtualFileSystem) loadArchive(name, path string) error {
	reader, err := hpi.OpenReader(path)
	if err != nil {
		return err
	}
	vfs.mountArchive(&archiveLayer{name: name, reader: reader, archivePath: path})
	return nil
}

// mountArchive adds an open archive's files to the VFS as one layer. Only
// entries a lookup can reach are registered: of several same-named entries
// the last, and nothing below a directory hidden by a later sibling of the
// same name. Entries whose path cannot be a safe key are left out.
func (vfs *VirtualFileSystem) mountArchive(layer *archiveLayer) {
	layer.seq = vfs.nextSeq()
	vfs.archives = append(vfs.archives, layer)
	vfs.layers = append(vfs.layers, &layerRecord{seq: layer.seq, label: layer.name, archive: layer})

	_ = layer.reader.Root().WalkReachable(func(entry *hpi.Entry) error {
		if entry.IsDir {
			return nil
		}
		normalized, ok := vfs.archiveKey(entry)
		if !ok {
			return nil
		}
		vfs.addDirs(normalized)
		vfs.applyLayer(normalized, FileLayer{
			Source:  layer.name,
			Size:    int64(entry.Size),
			seq:     layer.seq,
			archive: layer,
			entry:   entry,
		})
		return nil
	})
}

// archiveKey returns the VFS key for an archive entry, or false when the entry
// is excluded or its path is not a safe key.
func (vfs *VirtualFileSystem) archiveKey(entry *hpi.Entry) (string, bool) {
	p := entry.FullPath()
	if !safeVFSPath(p) || vfs.ShouldExclude(p, false) {
		return "", false
	}
	return vfs.normalizePath(p), true
}

// safeVFSPath reports whether every '/'- or '\'-separated segment of p is
// non-empty and neither "." nor "..".
func safeVFSPath(p string) bool {
	if p == "" {
		return false
	}
	for _, segment := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// scanLooseFrom scans for loose (non-archive) physical files under basePath and
// records them as a single layer with the given source label. When
// reportNested is set, archives found below the top level are reported as
// skipped (game-order discovery never mounts them).
func (vfs *VirtualFileSystem) scanLooseFrom(basePath, label string, reportNested bool) (*layerRecord, error) {
	rec := &layerRecord{seq: vfs.nextSeq(), label: label}
	vfs.layers = append(vfs.layers, rec)
	exts := vfs.archiveExtSet()

	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == basePath {
				return err
			}
			// An unreadable subdirectory hides only its own contents.
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// Skip archive files; they are handled by archive discovery.
		if !info.IsDir() && exts[common.ToLowerASCII(filepath.Ext(path))] {
			if reportNested && strings.ContainsRune(filepath.ToSlash(relPath), '/') && !vfs.ShouldExclude(relPath, false) {
				vfs.skip(filepath.Base(path), path, SkipSubdirectory, "game-order discovery mounts only archives in the top-level game directory")
			}
			return nil
		}

		if vfs.ShouldExclude(relPath, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		normalized := vfs.normalizePath(relPath)
		if info.IsDir() {
			vfs.directories[normalized] = true
			return nil
		}

		vfs.addDirs(normalized)
		vfs.applyLayer(normalized, FileLayer{
			Source:   label,
			Size:     info.Size(),
			seq:      rec.seq,
			physPath: path,
		})
		rec.loose = append(rec.loose, looseFile{
			key:      normalized,
			stored:   filepath.ToSlash(relPath),
			physPath: path,
			size:     info.Size(),
		})
		return nil
	})
	return rec, err
}

// normalizePath normalizes a path for storage in the VFS: '\' becomes '/' on
// every host, one leading '/' is dropped and, unless CaseSensitive is set,
// the ASCII letters A-Z are lower-cased. Other bytes are kept as they are, so
// names in a single-byte code page that differ only in accented letters stay
// distinct.
func (vfs *VirtualFileSystem) normalizePath(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Case sensitivity
	if !vfs.config.CaseSensitive {
		path = common.ToLowerASCII(path)
	}

	return path
}

// ShouldExclude checks if a file/directory should be excluded based on config
func (vfs *VirtualFileSystem) ShouldExclude(filePath string, isDir bool) bool {
	// Normalize path for consistent checking
	normalizedPath := vfs.normalizePath(filePath)
	parts := strings.Split(normalizedPath, "/")

	// Check if any directory in the path is excluded (case-insensitive)
	for _, part := range parts {
		for _, excludeDir := range vfs.config.ExcludeDirectories {
			if strings.EqualFold(part, excludeDir) {
				return true
			}
		}
	}

	// Check file-specific exclusions (only for files, not directories)
	if !isDir {
		// Get just the filename (without directory path)
		filename := path.Base(normalizedPath)
		filenameLower := strings.ToLower(filename)

		// Check file extension (case-insensitive)
		ext := strings.ToLower(path.Ext(filename))
		for _, excludeExt := range vfs.config.ExcludeExtensions {
			// Ensure extension starts with a dot
			excludeExtLower := strings.ToLower(excludeExt)
			if !strings.HasPrefix(excludeExtLower, ".") {
				excludeExtLower = "." + excludeExtLower
			}
			if ext == excludeExtLower {
				return true
			}
		}

		// Check filename prefix (case-insensitive)
		for _, prefix := range vfs.config.ExcludePrefixes {
			if strings.HasPrefix(filenameLower, strings.ToLower(prefix)) {
				return true
			}
		}
	}

	return false
}

// Open opens a file for reading
func (vfs *VirtualFileSystem) Open(path string) (io.ReadCloser, error) {
	normalized := vfs.normalizePath(path)

	vfs.filesMu.RLock()
	file, exists := vfs.files[normalized]
	physicalPath := vfs.physicalFiles[normalized]
	vfs.filesMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("file not found: %s", path)
	}

	// Physical file?
	if file.source == "disk" {
		return os.Open(physicalPath)
	}

	// Archive file
	if file.archive == nil || file.entry == nil {
		return nil, fmt.Errorf("no archive for file: %s", path)
	}

	// Lock the archive reader for thread-safe access
	file.archive.mu.Lock()
	defer file.archive.mu.Unlock()

	return file.archive.reader.OpenEntry(file.entry)
}

// Exists checks if a file or directory exists
func (vfs *VirtualFileSystem) Exists(path string) bool {
	normalized := vfs.normalizePath(path)

	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()

	if _, exists := vfs.files[normalized]; exists {
		return true
	}
	return vfs.directories[normalized]
}

// IsDir checks if a path is a directory
func (vfs *VirtualFileSystem) IsDir(path string) bool {
	normalized := vfs.normalizePath(path)
	// Root is always a directory
	if normalized == "" {
		return true
	}
	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()
	return vfs.directories[normalized]
}

// Stat returns information about a file
func (vfs *VirtualFileSystem) Stat(path string) (*FileInfo, error) {
	normalized := vfs.normalizePath(path)

	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()

	// Check if it's a file
	if file, exists := vfs.files[normalized]; exists {
		return &FileInfo{
			Path:   file.path,
			Size:   file.size,
			Source: file.source,
			IsDir:  false,
		}, nil
	}

	// Check if it's a directory
	if vfs.directories[normalized] {
		return &FileInfo{
			Path:   normalized,
			Size:   0,
			Source: "vfs",
			IsDir:  true,
		}, nil
	}

	return nil, fmt.Errorf("path not found: %s", path)
}

// List returns all files in the VFS, sorted, each once. It is not the game's
// enumeration order; see ListGameOrder for that.
func (vfs *VirtualFileSystem) List() []string {
	vfs.filesMu.RLock()
	files := make([]string, 0, len(vfs.files))
	for path := range vfs.files {
		files = append(files, path)
	}
	vfs.filesMu.RUnlock()

	sort.Strings(files)
	return files
}

// ListDir returns files in a specific directory
func (vfs *VirtualFileSystem) ListDir(dir string) ([]string, error) {
	normalized := vfs.normalizePath(dir)

	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()

	if !vfs.directories[normalized] && normalized != "" && normalized != "." {
		return nil, fmt.Errorf("not a directory: %s", dir)
	}

	prefix := normalized
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	files := make([]string, 0)
	seen := make(map[string]bool)

	// Find direct children among files
	for path := range vfs.files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(path, prefix)
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 && parts[0] != "" {
			child := parts[0]
			if !seen[child] {
				files = append(files, child)
				seen[child] = true
			}
		}
	}

	// Add directories
	for dirPath := range vfs.directories {
		if !strings.HasPrefix(dirPath, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(dirPath, prefix)
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 && parts[0] != "" {
			child := parts[0]
			if !seen[child] {
				files = append(files, child)
				seen[child] = true
			}
		}
	}

	sort.Strings(files)
	return files, nil
}

// Walk walks the entire filesystem tree
func (vfs *VirtualFileSystem) Walk(fn func(path string, info *FileInfo) error) error {
	// Snapshot all paths under the read lock, then Stat each without holding it.
	vfs.filesMu.RLock()
	allPaths := make(map[string]bool, len(vfs.files)+len(vfs.directories))
	for path := range vfs.files {
		allPaths[path] = true
	}
	for dir := range vfs.directories {
		allPaths[dir] = true
	}
	vfs.filesMu.RUnlock()

	paths := make([]string, 0, len(allPaths))
	for path := range allPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		info, err := vfs.Stat(path)
		if err != nil {
			// The path may have been removed between snapshot and Stat; skip it.
			continue
		}
		if err := fn(path, info); err != nil {
			return err
		}
	}

	return nil
}

// ReadFile reads an entire file into memory
func (vfs *VirtualFileSystem) ReadFile(path string) ([]byte, error) {
	reader, err := vfs.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err == nil {
		vfs.recordBytesRead(int64(len(data)))
	}
	return data, err
}

// ReadFileFromSource reads a file from a specific source layer.
// sourceName can be the physical/overlay label or an archive name like "totala1.hpi".
func (vfs *VirtualFileSystem) ReadFileFromSource(path, sourceName string) ([]byte, error) {
	normalized := vfs.normalizePath(path)

	// Resolve the layer under the read lock.
	vfs.filesMu.RLock()
	var found FileLayer
	ok := false
	for _, l := range vfs.fileLayers[normalized] {
		if l.Source == sourceName {
			found, ok = l, true
			break
		}
	}
	vfs.filesMu.RUnlock()

	if ok && found.archive == nil {
		data, err := os.ReadFile(found.physPath)
		if err == nil {
			vfs.recordBytesRead(int64(len(data)))
		}
		return data, err
	}
	if ok {
		layer := found.archive
		layer.mu.Lock()
		defer layer.mu.Unlock()

		rc, err := layer.reader.OpenEntry(found.entry)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()

		data, err := io.ReadAll(rc)
		if err == nil {
			vfs.recordBytesRead(int64(len(data)))
		}
		return data, err
	}

	for _, layer := range vfs.archives {
		if layer.name == sourceName {
			return nil, fmt.Errorf("file not found in %s", sourceName)
		}
	}
	return nil, fmt.Errorf("source not found: %s", sourceName)
}

// Archives returns the names of the loaded archives in load order: an archive
// later in the list takes precedence over an earlier one. MountOrder gives the
// same archives in lookup order with their paths.
func (vfs *VirtualFileSystem) Archives() []string {
	archives := make([]string, len(vfs.archives))
	for i, archive := range vfs.archives {
		archives[i] = archive.name
	}
	return archives
}

// Stats returns statistics about the VFS
func (vfs *VirtualFileSystem) Stats() map[string]interface{} {
	archiveFileCount := 0
	physicalFileCount := 0
	totalUnpackedSize := int64(0)
	totalPackedSize := int64(0)

	vfs.filesMu.RLock()
	for _, file := range vfs.files {
		if file.source == "disk" {
			physicalFileCount++
		} else {
			archiveFileCount++
		}
		totalUnpackedSize += file.size
	}
	totalFiles := len(vfs.files)
	dirCount := len(vfs.directories)
	vfs.filesMu.RUnlock()

	// Calculate packed size from archive files (archives are immutable post-load).
	for _, layer := range vfs.archives {
		if fileInfo, err := os.Stat(layer.archivePath); err == nil {
			totalPackedSize += fileInfo.Size()
		}
	}

	// Calculate compression ratio
	compressionRatio := 0.0
	if totalUnpackedSize > 0 {
		compressionRatio = (1.0 - float64(totalPackedSize)/float64(totalUnpackedSize)) * 100
	}

	modes := make([]string, len(vfs.discovery))
	for i, m := range vfs.discovery {
		modes[i] = m.String()
	}

	return map[string]interface{}{
		"archives":            len(vfs.archives),
		"total_files":         totalFiles,
		"archive_files":       archiveFileCount,
		"physical_files":      physicalFileCount,
		"directories":         dirCount,
		"total_unpacked_size": totalUnpackedSize,
		"total_packed_size":   totalPackedSize,
		"compression_ratio":   compressionRatio,
		"base_path":           vfs.basePath,
		"archive_names":       vfs.Archives(),
		"skipped_archives":    len(vfs.skipped),
		"discovery":           strings.Join(modes, ","),
	}
}

// DirectoryStats returns statistics for a specific directory
func (vfs *VirtualFileSystem) DirectoryStats(dirPath string) map[string]interface{} {
	dirPath = vfs.normalizePath(dirPath)

	fileCount := 0
	subdirCount := 0
	totalSize := int64(0)

	vfs.filesMu.RLock()
	for p, file := range vfs.files {
		if path.Dir(p) == dirPath {
			fileCount++
			totalSize += file.size
		}
	}
	for dir := range vfs.directories {
		if path.Dir(dir) == dirPath {
			subdirCount++
		}
	}
	vfs.filesMu.RUnlock()

	return map[string]interface{}{
		"path":           dirPath,
		"files":          fileCount,
		"subdirectories": subdirCount,
		"total_size":     totalSize,
	}
}

// RecursiveDirectoryStats returns statistics for a directory and all subdirectories
func (vfs *VirtualFileSystem) RecursiveDirectoryStats(dirPath string) map[string]interface{} {
	dirPath = vfs.normalizePath(dirPath)

	fileCount := 0
	subdirCount := 0
	totalSize := int64(0)

	prefix := dirPath
	if prefix != "" && prefix != "." {
		prefix = prefix + "/"
	} else {
		prefix = ""
	}

	vfs.filesMu.RLock()
	for path, file := range vfs.files {
		if prefix == "" || strings.HasPrefix(path, prefix) {
			fileCount++
			totalSize += file.size
		}
	}
	for dir := range vfs.directories {
		if prefix == "" || strings.HasPrefix(dir, prefix) {
			if dir != dirPath { // Don't count the directory itself
				subdirCount++
			}
		}
	}
	vfs.filesMu.RUnlock()

	return map[string]interface{}{
		"path":           dirPath,
		"files":          fileCount,
		"subdirectories": subdirCount,
		"total_size":     totalSize,
	}
}

// FileLayer represents a single layer containing a file
type FileLayer struct {
	Source   string // Archive name or physical/overlay label
	Priority int    // Layer priority (lower = higher priority); assigned by GetFileLayers
	Size     int64  // File size in this layer

	seq      int           // Monotonic load sequence (higher = higher priority)
	archive  *archiveLayer // Set for archive layers
	entry    *hpi.Entry    // The archive entry, for archive layers
	physPath string        // Set for physical/overlay layers
}

// GetFileLayers returns all layers containing this file, ordered by priority
// (index 0 = highest priority = the active version).
func (vfs *VirtualFileSystem) GetFileLayers(path string) []FileLayer {
	normalized := vfs.normalizePath(path)

	vfs.filesMu.RLock()
	layers := vfs.fileLayers[normalized]
	result := make([]FileLayer, len(layers))
	copy(result, layers)
	vfs.filesMu.RUnlock()

	// Higher load sequence = higher priority (lower Priority number).
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].seq > result[j].seq
	})
	for i := range result {
		result[i].Priority = i
	}

	return result
}

// startMD5Calculation starts background MD5 calculation for all files
// Uses a worker pool of 10 goroutines to parallelize the work
func (vfs *VirtualFileSystem) startMD5Calculation() {
	const numWorkers = 10

	filePaths := make(chan string, 100)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range filePaths {
				vfs.calculateFileMD5(path)
			}
		}()
	}

	// Snapshot the path set under the read lock, then feed the workers.
	go func() {
		vfs.filesMu.RLock()
		paths := make([]string, 0, len(vfs.files))
		for path := range vfs.files {
			paths = append(paths, path)
		}
		vfs.filesMu.RUnlock()

		for _, path := range paths {
			filePaths <- path
		}
		close(filePaths)
		wg.Wait()
	}()
}

// calculateFileMD5 calculates and stores the MD5 hash for a single file
func (vfs *VirtualFileSystem) calculateFileMD5(path string) {
	reader, err := vfs.Open(path)
	if err != nil {
		return // Skip files that can't be opened
	}
	defer func() { _ = reader.Close() }()

	hash := md5.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return // Skip files with read errors
	}

	md5Sum := fmt.Sprintf("%x", hash.Sum(nil))
	vfs.md5Mutex.Lock()
	vfs.md5Hashes[path] = md5Sum
	vfs.md5Mutex.Unlock()
}

// GetMD5 returns the MD5 hash for a file if it has been calculated
// Returns (hash, true) if available, ("", false) if not yet calculated
func (vfs *VirtualFileSystem) GetMD5(path string) (string, bool) {
	normalized := vfs.normalizePath(path)

	vfs.md5Mutex.RLock()
	hash, exists := vfs.md5Hashes[normalized]
	vfs.md5Mutex.RUnlock()

	return hash, exists
}

// SetMetricsCallback sets a callback function for tracking bytes read
func (vfs *VirtualFileSystem) SetMetricsCallback(callback func(bytes int64)) {
	vfs.metricsMutex.Lock()
	defer vfs.metricsMutex.Unlock()
	vfs.metricsCallback = callback
}

// recordBytesRead calls the metrics callback if set
func (vfs *VirtualFileSystem) recordBytesRead(bytes int64) {
	vfs.metricsMutex.RLock()
	callback := vfs.metricsCallback
	vfs.metricsMutex.RUnlock()

	if callback != nil {
		callback(bytes)
	}
}
