package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultWritableLabel is the FileLayer.Source used for the writable overlay
// when a Source does not specify its own Label.
const defaultWritableLabel = "Workspace"

// SourceKind identifies how a layer source is loaded.
type SourceKind int

const (
	// SourceContextDir is a game-install directory: its archives are loaded
	// with the configured discovery mode (see Config.Discovery), then its
	// loose physical files are overlaid.
	SourceContextDir SourceKind = iota
	// SourceArchive is a single archive file of either version. It is
	// mounted as given, whatever the discovery mode.
	SourceArchive
	// SourceLooseDir is a plain directory of loose files (no archives), such as
	// a workspace's writable work folder.
	SourceLooseDir
)

// Source describes one layer in a VFS stack.
type Source struct {
	Kind SourceKind
	Path string

	// Writable marks this source as the writable overlay. At most one source
	// may be writable and it must be the top (highest-priority) layer and a
	// SourceLooseDir.
	Writable bool

	// Label overrides the FileLayer.Source label reported for this source. When
	// empty, a sensible default is chosen (the directory base name, or
	// "Physical Filesystem" for loose files inside a context directory).
	Label string
}

// NewLayered creates a virtual filesystem from an ordered stack of sources.
// sources[0] is the highest-priority (top) layer; later entries are lower
// priority. Files present in more than one source resolve to the top-most one.
// Within a SourceContextDir, loose files rank above the directory's archives,
// and the archives rank among themselves by Config.Discovery.
//
// A single writable SourceLooseDir may be supplied as sources[0] to make the
// VFS support copy-on-write edits (see WriteFile/EnsureLocal/Remove).
func NewLayered(sources []Source, config *Config) (*VirtualFileSystem, error) {
	if config == nil {
		config = &Config{}
	}
	if len(config.Extensions) == 0 {
		config.Extensions = []string{".hpi", ".ccx", ".gp3", ".ufo"}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("filesystem: no sources provided")
	}

	vfs := newVFS(config)

	// Display base path: prefer the first context directory, else the first source.
	vfs.basePath = sources[0].Path
	for _, s := range sources {
		if s.Kind == SourceContextDir {
			vfs.basePath = s.Path
			break
		}
	}

	// Validate and wire up the writable overlay, if any.
	for i, s := range sources {
		if !s.Writable {
			continue
		}
		if s.Kind != SourceLooseDir {
			return nil, fmt.Errorf("filesystem: writable source must be a loose directory")
		}
		if i != 0 {
			return nil, fmt.Errorf("filesystem: writable source must be the top layer")
		}
		if vfs.writeDir != "" {
			return nil, fmt.Errorf("filesystem: only one writable source allowed")
		}
		vfs.writeDir = s.Path
		vfs.writableLabel = s.Label
		if vfs.writableLabel == "" {
			vfs.writableLabel = defaultWritableLabel
		}
	}

	if vfs.writeDir != "" {
		if err := os.MkdirAll(vfs.writeDir, 0o755); err != nil {
			return nil, fmt.Errorf("filesystem: create work folder: %w", err)
		}
	}

	// Apply lowest-priority sources first so the top layer overrides them.
	for i := len(sources) - 1; i >= 0; i-- {
		if err := vfs.loadSource(sources[i]); err != nil {
			if !config.SkipErrors {
				_ = vfs.Close()
				return nil, err
			}
		}
	}

	vfs.startMD5Calculation()
	return vfs, nil
}

// loadSource applies a single source's contents into the VFS.
func (vfs *VirtualFileSystem) loadSource(src Source) error {
	switch src.Kind {
	case SourceContextDir:
		return vfs.loadContextDir(src.Path)

	case SourceArchive:
		return vfs.loadArchive(filepath.Base(src.Path), src.Path)

	case SourceLooseDir:
		label := src.Label
		if src.Writable {
			label = vfs.writableLabel
		} else if label == "" {
			label = filepath.Base(src.Path)
		}
		rec, err := vfs.scanLooseFrom(src.Path, label, false)
		if src.Writable {
			vfs.writableLayer = rec
		}
		return err

	default:
		return fmt.Errorf("filesystem: unknown source kind %d", src.Kind)
	}
}

// writablePath checks a path for the writable overlay and returns it with
// '/' separators and no leading '/'. Every segment must be non-empty and
// neither "." nor "..", so the file stays inside the work folder and its key
// names exactly one file.
func writablePath(p string) (string, error) {
	rel := strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "/")
	if !safeVFSPath(rel) {
		return "", fmt.Errorf("filesystem: invalid path %q: empty, \".\" or \"..\" segment", p)
	}
	return rel, nil
}

// diskPathFor maps a virtual path to its on-disk location in the work folder,
// preserving the caller's casing for the physical file name.
func (vfs *VirtualFileSystem) diskPathFor(path string) (string, error) {
	rel, err := writablePath(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(vfs.writeDir, filepath.FromSlash(rel)), nil
}

// WriteFile writes data to the writable overlay layer, overriding any
// lower-layer version of the same path. It fails if the VFS is read-only, and
// for a path with an empty, "." or ".." segment.
func (vfs *VirtualFileSystem) WriteFile(path string, data []byte) error {
	if vfs.writeDir == "" {
		return fmt.Errorf("filesystem: read-only (no writable layer)")
	}

	diskPath, err := vfs.diskPathFor(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(diskPath, data, 0o644); err != nil {
		return err
	}

	normalized := vfs.normalizePath(path)

	vfs.filesMu.Lock()
	vfs.addDirs(normalized)
	vfs.removeLayerBySourceLocked(normalized, vfs.writableLabel)
	layer := FileLayer{
		Source:   vfs.writableLabel,
		Size:     int64(len(data)),
		seq:      vfs.nextSeq(),
		physPath: diskPath,
	}
	vfs.fileLayers[normalized] = append(vfs.fileLayers[normalized], layer)
	vfs.setActiveFromLayer(normalized, layer)
	if rec := vfs.writableLayer; rec != nil {
		rec.removeLoose(normalized)
		rel, _ := writablePath(path)
		rec.loose = append(rec.loose, looseFile{key: normalized, stored: rel, physPath: diskPath, size: int64(len(data))})
	}
	vfs.filesMu.Unlock()

	vfs.md5Mutex.Lock()
	delete(vfs.md5Hashes, normalized)
	vfs.md5Mutex.Unlock()

	return nil
}

// removeLoose drops every loose file with the given key from the layer.
func (rec *layerRecord) removeLoose(key string) {
	kept := rec.loose[:0]
	for _, f := range rec.loose {
		if f.key != key {
			kept = append(kept, f)
		}
	}
	rec.loose = kept
}

// IsLocal reports whether a path is backed by the writable overlay layer
// (either net-new or an override of a base-layer file).
func (vfs *VirtualFileSystem) IsLocal(path string) bool {
	if vfs.writeDir == "" {
		return false
	}
	normalized := vfs.normalizePath(path)

	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()
	for _, l := range vfs.fileLayers[normalized] {
		if l.Source == vfs.writableLabel {
			return true
		}
	}
	return false
}

// EnsureLocal guarantees that an existing file has a writable copy in the work
// folder (copy-on-write), so subsequent edits land in the overlay. It is a
// no-op if the file is already local, and errors if the file does not exist.
func (vfs *VirtualFileSystem) EnsureLocal(path string) error {
	if vfs.writeDir == "" {
		return fmt.Errorf("filesystem: read-only (no writable layer)")
	}
	if _, err := writablePath(path); err != nil {
		return err
	}
	if vfs.IsLocal(path) {
		return nil
	}
	if !vfs.Exists(path) {
		return fmt.Errorf("file not found: %s", path)
	}
	data, err := vfs.ReadFile(path)
	if err != nil {
		return err
	}
	return vfs.WriteFile(path, data)
}

// Remove deletes a path from the writable overlay. Per the no-whiteout rule:
//   - a net-new local file is removed entirely;
//   - an override reverts to the underlying base version;
//   - a file that exists only in a read-only base layer cannot be removed.
func (vfs *VirtualFileSystem) Remove(path string) error {
	if vfs.writeDir == "" {
		return fmt.Errorf("filesystem: read-only (no writable layer)")
	}
	if _, err := writablePath(path); err != nil {
		return err
	}
	normalized := vfs.normalizePath(path)

	vfs.filesMu.Lock()
	layers := vfs.fileLayers[normalized]
	if len(layers) == 0 {
		vfs.filesMu.Unlock()
		return fmt.Errorf("file not found: %s", path)
	}

	var localPhys string
	hasLocal := false
	for _, l := range layers {
		if l.Source == vfs.writableLabel {
			hasLocal = true
			localPhys = l.physPath
		}
	}
	if !hasLocal {
		vfs.filesMu.Unlock()
		return fmt.Errorf("cannot delete %q: it belongs to a read-only base layer; exclude it via content settings (e.g. sidedata) instead", path)
	}

	if localPhys != "" {
		_ = os.Remove(localPhys)
	}
	if rec := vfs.writableLayer; rec != nil {
		rec.removeLoose(normalized)
	}

	remaining := make([]FileLayer, 0, len(layers))
	for _, l := range layers {
		if l.Source != vfs.writableLabel {
			remaining = append(remaining, l)
		}
	}

	if len(remaining) == 0 {
		delete(vfs.fileLayers, normalized)
		delete(vfs.files, normalized)
		delete(vfs.physicalFiles, normalized)
	} else {
		vfs.fileLayers[normalized] = remaining
		best := remaining[0]
		for _, l := range remaining[1:] {
			if l.seq > best.seq {
				best = l
			}
		}
		vfs.setActiveFromLayer(normalized, best)
	}
	vfs.filesMu.Unlock()

	vfs.md5Mutex.Lock()
	delete(vfs.md5Hashes, normalized)
	vfs.md5Mutex.Unlock()

	return nil
}

// removeLayerBySourceLocked drops all layers for a path that come from the given
// source. Callers must hold filesMu.
func (vfs *VirtualFileSystem) removeLayerBySourceLocked(normalized, source string) {
	layers := vfs.fileLayers[normalized]
	if len(layers) == 0 {
		return
	}
	filtered := make([]FileLayer, 0, len(layers))
	for _, l := range layers {
		if l.Source != source {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) == 0 {
		delete(vfs.fileLayers, normalized)
		return
	}
	vfs.fileLayers[normalized] = filtered
}

// LocalLayerInfo reports whether the path has a copy in the writable layer
// (i.e. Remove would succeed) and how many layers carry it in total. A
// deletable file with more than one layer is an OVERRIDE — removing it
// reverts to the base version rather than erasing the path.
func (vfs *VirtualFileSystem) LocalLayerInfo(path string) (hasLocal bool, layers int) {
	if vfs.writeDir == "" {
		return false, 0
	}
	normalized := vfs.normalizePath(path)
	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()
	ls := vfs.fileLayers[normalized]
	for _, l := range ls {
		if l.Source == vfs.writableLabel {
			hasLocal = true
		}
	}
	return hasLocal, len(ls)
}
