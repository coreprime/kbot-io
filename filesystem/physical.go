package filesystem

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Ensure PhysicalFileSystem implements FileSystem
var _ FileSystem = (*PhysicalFileSystem)(nil)

// PhysicalFileSystem implements FileSystem for local disk access
type PhysicalFileSystem struct {
	basePath string
}

// NewPhysicalFileSystem creates a filesystem that reads from a local directory
func NewPhysicalFileSystem(basePath string) (*PhysicalFileSystem, error) {
	// Normalize the path
	absPath, err := filepath.Abs(basePath)
	if err != nil {
		return nil, err
	}

	// Verify the path exists
	if _, err := os.Stat(absPath); err != nil {
		return nil, err
	}

	return &PhysicalFileSystem{
		basePath: absPath,
	}, nil
}

// resolve maps a caller path to a location under the base directory. '\'
// and '/' both separate segments. A path that would leave the base directory
// once cleaned (for example "../x" or "a/../../x") is refused.
func (pfs *PhysicalFileSystem) resolve(path string) (string, error) {
	rel := filepath.FromSlash(strings.ReplaceAll(path, `\`, "/"))
	full := filepath.Join(pfs.basePath, rel)
	within, err := filepath.Rel(pfs.basePath, full)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", &fs.PathError{Op: "open", Path: path, Err: fs.ErrInvalid}
	}
	return full, nil
}

// Open opens a file for reading. Paths that leave the base directory are
// refused.
func (pfs *PhysicalFileSystem) Open(path string) (io.ReadCloser, error) {
	fullPath, err := pfs.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Open(fullPath)
}

// ReadFile reads the entire file content. Paths that leave the base
// directory are refused.
func (pfs *PhysicalFileSystem) ReadFile(path string) ([]byte, error) {
	fullPath, err := pfs.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(fullPath)
}

// Exists checks if a file exists. It is false for paths that leave the base
// directory.
func (pfs *PhysicalFileSystem) Exists(path string) bool {
	fullPath, err := pfs.resolve(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(fullPath)
	return err == nil
}

// List returns all file paths (recursively walks the directory)
func (pfs *PhysicalFileSystem) List() []string {
	var files []string

	_ = filepath.Walk(pfs.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(pfs.basePath, path)
		if err != nil {
			return nil
		}

		files = append(files, relPath)
		return nil
	})

	return files
}

// Close closes any resources (no-op for physical filesystem)
func (pfs *PhysicalFileSystem) Close() error {
	return nil
}
