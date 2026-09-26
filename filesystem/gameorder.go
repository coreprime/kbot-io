package filesystem

import (
	"sort"
	"strings"

	"github.com/coreprime/kbot-io/formats/hpi"
	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// GameOrderEntry is one file in a ListGameOrder listing.
type GameOrderEntry struct {
	// Path is the VFS key, as List returns it. ReadFile(Path) returns the
	// active copy, which is what the game's loaders read for every entry
	// they enumerate, duplicates included.
	Path string
	// StoredPath is the path as spelled in the archive or on disk, with '/'
	// separators.
	StoredPath string
	// Source is the layer label: the archive file name, or the loose layer's
	// label.
	Source string
	// Size is this copy's size in bytes.
	Size int64
	// Active reports whether this copy is the one ReadFile(Path) returns.
	Active bool
}

// ListGameOrder lists the files in dir whose names end with ext (compared
// ignoring ASCII letter case; empty matches every file), in the order the
// game enumerates them. With recursive set, subdirectories are included.
//
// The order is layer by layer, highest precedence first: the loose files of a
// game directory, then its archives in mount order (see MountOrder), and so
// on down the sources of NewLayered.
//
//   - Loose files are listed by name with ASCII letters upper-cased, the
//     order the game's directory enumeration returns; a subdirectory's files
//     come where the subdirectory's name sorts.
//   - An archive lists the entries of the directory a lookup of dir resolves
//     to in that archive, in stored order. With recursive set, each
//     subdirectory entry is followed by the files of the directory a lookup
//     of its path resolves to in the same archive.
//   - Duplicates are kept: a path held by several archives, or stored twice
//     in one archive, appears once per copy. An archive file that a loose
//     file of a higher layer replaces is left out, as the game hides it.
//
// Loaders that keep the first definition they meet, such as feature
// definitions, reach the game's result by reading each entry's Path in this
// order. It is not the order of List, which is sorted and de-duplicated.
func (vfs *VirtualFileSystem) ListGameOrder(dir, ext string, recursive bool) []GameOrderEntry {
	dirKey := strings.TrimSuffix(vfs.normalizePath(dir), "/")
	prefix := ""
	if dirKey != "" {
		prefix = dirKey + "/"
	}
	match := func(name string) bool {
		return ext == "" || (len(name) >= len(ext) && common.EqualFoldASCII(name[len(name)-len(ext):], ext))
	}

	vfs.filesMu.RLock()
	defer vfs.filesMu.RUnlock()

	var out []GameOrderEntry
	for _, rec := range vfs.layersByPrecedence() {
		if rec.archive == nil {
			out = vfs.appendLoose(out, rec, prefix, recursive, match)
			continue
		}
		root := rec.archive.reader.Root()
		if root == nil {
			continue
		}
		start := root
		if dirKey != "" {
			start = root.Find(dirKey)
		}
		if start == nil || !start.IsDir {
			continue
		}
		out = vfs.appendArchiveDir(out, rec, root, start, recursive, match)
	}
	return out
}

// appendLoose adds a loose layer's matching files. Callers hold filesMu.
func (vfs *VirtualFileSystem) appendLoose(out []GameOrderEntry, rec *layerRecord, prefix string, recursive bool, match func(string) bool) []GameOrderEntry {
	var files []looseFile
	for _, f := range rec.loose {
		if !strings.HasPrefix(f.key, prefix) {
			continue
		}
		rest := f.key[len(prefix):]
		if !recursive && strings.ContainsRune(rest, '/') {
			continue
		}
		name := rest[strings.LastIndexByte(rest, '/')+1:]
		if !match(name) {
			continue
		}
		files = append(files, f)
	}
	sort.SliceStable(files, func(i, j int) bool {
		return gameOrderLess(files[i].stored, files[j].stored)
	})
	for _, f := range files {
		out = append(out, GameOrderEntry{
			Path:       f.key,
			StoredPath: f.stored,
			Source:     rec.label,
			Size:       f.size,
			Active:     vfs.files[f.key] != nil && vfs.files[f.key].archive == nil && vfs.physicalFiles[f.key] == f.physPath,
		})
	}
	return out
}

// gameOrderLess compares two '/'-separated paths segment by segment, each
// segment by its bytes after upper-casing ASCII letters. This is the order of
// a depth-first walk that lists each directory in the game's enumeration
// order.
func gameOrderLess(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ua, ub := common.ToUpperASCII(as[i]), common.ToUpperASCII(bs[i])
		if ua != ub {
			return ua < ub
		}
	}
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	return a < b
}

// appendArchiveDir adds the matching files of one archive directory, in
// stored order. Callers hold filesMu.
func (vfs *VirtualFileSystem) appendArchiveDir(out []GameOrderEntry, rec *layerRecord, root, dir *hpi.Entry, recursive bool, match func(string) bool) []GameOrderEntry {
	for _, child := range dir.Children {
		if child.IsDir {
			if !recursive {
				continue
			}
			if sub := root.Find(child.FullPath()); sub != nil && sub.IsDir {
				out = vfs.appendArchiveDir(out, rec, root, sub, recursive, match)
			}
			continue
		}
		if !match(child.Name) {
			continue
		}
		key, ok := vfs.archiveKey(child)
		if !ok {
			continue
		}
		if root.Find(child.FullPath()) == child && vfs.hiddenByLoose(key, rec.seq) {
			continue
		}
		active := vfs.files[key]
		out = append(out, GameOrderEntry{
			Path:       key,
			StoredPath: child.FullPath(),
			Source:     rec.label,
			Size:       int64(child.Size),
			Active:     active != nil && active.archive == rec.archive && active.entry == child,
		})
	}
	return out
}

// hiddenByLoose reports whether a loose file of a layer above seq holds key.
// Callers hold filesMu.
func (vfs *VirtualFileSystem) hiddenByLoose(key string, seq int) bool {
	for _, l := range vfs.fileLayers[key] {
		if l.archive == nil && l.seq > seq {
			return true
		}
	}
	return false
}
