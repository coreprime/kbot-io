package common

import (
	"fmt"
	"strings"
)

// Entry represents a file or directory in an HPI archive. The same node type
// is produced by both the v1 and v2 readers so callers can walk either tree
// without caring about the on-disk version.
//
// Children keep the order in which the archive stores them, including
// duplicate names. A lookup reaches only the last of several children whose
// names match (see Find), but every stored record stays in the tree.
//
// For v1 archives, Size is the file's decompressed size and CompType is the
// record's compression byte: 0 for a stored entry, any other value for a
// chunked one.
//
// For v2 archives (TA: Kingdoms), Size is the decompressed size and
// CompressedSize is the on-disk SQSH chunk size; CompressedSize == 0 means the
// payload is stored uncompressed.
type Entry struct {
	Name           string
	IsDir          bool
	Offset         uint32
	Size           uint32
	CompressedSize uint32
	CompType       uint8
	Children       []*Entry
	Parent         *Entry
}

// FullPath returns the entry's path from the archive root: the raw names of
// its ancestors and itself joined with '/'. Names are not cleaned, so an
// entry stored as ".." keeps that segment and Find(e.FullPath()) resolves the
// same record whenever the entry is reachable.
func (e *Entry) FullPath() string {
	if e.Parent == nil {
		return e.Name
	}
	parentPath := e.Parent.FullPath()
	if parentPath == "" && e.Parent.Parent == nil {
		return e.Name
	}
	return parentPath + "/" + e.Name
}

// Find locates an entry by path using the game's lookup rules:
//
//   - both '\' and '/' separate segments, on every host;
//   - each segment matches the last child whose name is equal ignoring ASCII
//     letter case (bytes 0x80 and above compare exactly);
//   - every segment but the last must resolve to a directory, and there is no
//     fallback to an earlier child of the same name;
//   - an empty segment only matches a child whose name is empty, so leading,
//     trailing or doubled separators normally make the lookup fail.
//
// Find("") returns the receiver. It returns nil when nothing matches.
func (e *Entry) Find(path string) *Entry {
	if path == "" {
		return e
	}
	current := e
	rest := path
	for {
		end := strings.IndexAny(rest, `\/`)
		segment := rest
		if end >= 0 {
			segment = rest[:end]
		}
		match := current.lastChild(segment)
		if match == nil {
			return nil
		}
		if end < 0 {
			return match
		}
		if !match.IsDir {
			return nil
		}
		current = match
		rest = rest[end+1:]
	}
}

// lastChild returns the last child whose name equals name ignoring ASCII
// letter case, or nil.
func (e *Entry) lastChild(name string) *Entry {
	for i := len(e.Children) - 1; i >= 0; i-- {
		if EqualFoldASCII(e.Children[i].Name, name) {
			return e.Children[i]
		}
	}
	return nil
}

// Walk traverses the entry tree depth-first, calling fn for each node.
// Every stored record is visited, including ones a lookup cannot reach.
func (e *Entry) Walk(fn func(*Entry) error) error {
	if err := fn(e); err != nil {
		return err
	}
	for _, child := range e.Children {
		if err := child.Walk(fn); err != nil {
			return err
		}
	}
	return nil
}

// WalkReachable traverses, depth-first in stored order, only the entries a
// lookup of their FullPath resolves to: the last of each group of
// case-insensitively equal sibling names, below directories that are
// themselves reachable. The receiver is visited first. For each reachable
// file, Find(entry.FullPath()) returns that entry.
func (e *Entry) WalkReachable(fn func(*Entry) error) error {
	if err := fn(e); err != nil {
		return err
	}
	if !e.IsDir {
		return nil
	}
	for _, child := range e.Children {
		if e.lastChild(child.Name) != child {
			continue
		}
		if err := child.WalkReachable(fn); err != nil {
			return err
		}
	}
	return nil
}

// Reachable reports whether a lookup of e's FullPath from the root resolves
// to e itself.
func (e *Entry) Reachable() bool {
	for node := e; node.Parent != nil; node = node.Parent {
		if !node.Parent.IsDir || node.Parent.lastChild(node.Name) != node {
			return false
		}
	}
	return true
}

// EqualFoldASCII reports whether a and b are equal when the ASCII letters
// A-Z and a-z are folded together. Other bytes, including every byte of 0x80
// or above, must match exactly; this is how the game compares archive and file
// names, which are code-page strings rather than UTF-8.
func EqualFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// ToLowerASCII lower-cases the ASCII letters A-Z in s and leaves every other
// byte untouched, so names in a single-byte code page keep their raw bytes.
func ToLowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				b[j] = lowerASCII(b[j])
			}
			return string(b)
		}
	}
	return s
}

// ToUpperASCII upper-cases the ASCII letters a-z in s and leaves every other
// byte untouched.
func ToUpperASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'a' && b[j] <= 'z' {
					b[j] -= 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// CleanArchivePath checks a path for writing into an archive and returns it
// with '/' separators. Both '\' and '/' separate segments on every host. Every
// segment must be non-empty, must not be "." or "..", must not contain a NUL
// byte and must be at most MaxNameLength bytes long, so the result has no
// leading, trailing or doubled separators.
func CleanArchivePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty archive path")
	}
	cleaned := strings.ReplaceAll(p, `\`, "/")
	for _, segment := range strings.Split(cleaned, "/") {
		switch {
		case segment == "":
			return "", fmt.Errorf("archive path %q has an empty segment", p)
		case segment == "." || segment == "..":
			return "", fmt.Errorf("archive path %q has a %q segment", p, segment)
		case strings.IndexByte(segment, 0) >= 0:
			return "", fmt.Errorf("archive path %q contains a NUL byte", p)
		case len(segment) > MaxNameLength:
			return "", fmt.Errorf("archive path %q has a name of %d bytes (limit %d)", p, len(segment), MaxNameLength)
		}
	}
	return cleaned, nil
}
