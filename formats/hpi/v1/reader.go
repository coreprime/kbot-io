// Package v1 reads and writes Total Annihilation HPI archives (version
// 0x00010000). The directory tree and every file payload are scrambled with
// HPI's position-dependent XOR cipher; file payloads are either stored as-is
// or split into 64 KiB blocks, each compressed in its own SQSH chunk.
//
// The reader follows the rules TA 3.1c applies when it reads an archive:
//
//   - The header key byte 0 or 0xFF means the archive is not encrypted; any
//     other byte is rotated left by two bits to give the XOR key.
//   - The directory block is header.DirectorySize bytes from the start of the
//     file; everything after the 20-byte header is decrypted. The root
//     directory sits at header.Offset. An offset of 0 is read as 20 (the byte
//     after the header); offsets 1-19 overlap the header and are refused.
//   - A directory's entry count is a signed 32-bit value; a negative count is
//     an empty directory. Only bit 0 of an entry's flag byte marks a
//     directory. A name runs to the next NUL byte anywhere in the directory
//     block. A directory reached twice (a cycle or a shared node) and nesting
//     deeper than common.MaxDirectoryDepth are refused.
//   - Lookups split on '\' and '/', fold ASCII letter case only, and take the
//     last matching name in each directory (see common.Entry.Find). Earlier
//     duplicates stay in the tree and in List but cannot be opened by path.
//   - A file record's compression byte 0 marks a stored entry. Any other value
//     marks a chunked entry: a table of ceil(size/65536) chunk sizes, then the
//     chunks. Chunk i starts after the table plus the sizes of chunks 0..i-1,
//     is bounded by its table entry, and fills bytes [i*65536, i*65536+65536)
//     of the file; a chunk that decodes short leaves zeros in the rest of its
//     block, and bytes past the file size are dropped. Each chunk is decoded
//     with common.DecodeBlock.
//
// The reader does not require the Cavedog copyright trailer, so archives the
// game would refuse can still be inspected and extracted; TrailerValid and
// hpi.Validate report whether the game would mount the archive.
package v1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// DefaultMaxEntrySize is the largest decoded file ReadOptions allows unless
// MaxEntrySize says otherwise. Retail files are a few megabytes at most; the
// bound stops a forged record from forcing a multi-gigabyte allocation.
const DefaultMaxEntrySize = 1 << 30

// ReadOptions adjusts how a v1 archive is read. The zero value reads the way
// TA 3.1c does.
type ReadOptions struct {
	// Strict turns the chunk irregularities the game tolerates into errors:
	// a zlib stream that is corrupt, fails its Adler-32 check, is cut short
	// or runs past the chunk's stated size (the game keeps the stated size),
	// and a chunk whose unpacked size does not exactly fill its 64 KiB block
	// (the game zero-fills the rest of the block, or drops the excess of the
	// last block).
	Strict bool

	// MaxEntrySize caps the decoded size of a single file. Zero means
	// DefaultMaxEntrySize.
	MaxEntrySize int64
}

// Reader reads a Total Annihilation (v1) HPI archive.
//
// A Reader is not safe for concurrent use: every read seeks the shared file
// handle, so callers serving an archive to multiple goroutines must serialize
// access.
type Reader struct {
	file       *os.File
	header     *common.Header
	root       *common.Entry
	decryptKey uint8
	fileSize   int64
	tail       []byte
	opts       ReadOptions
}

// Open opens a v1 HPI archive for reading with the game's rules.
func Open(path string) (*Reader, error) {
	return OpenWithOptions(path, ReadOptions{})
}

// OpenWithOptions opens a v1 HPI archive for reading with the given options.
func OpenWithOptions(path string, opts ReadOptions) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	r := &Reader{file: file, opts: opts}
	if err := r.readHeader(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := r.readDirectory(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return r, nil
}

// Version reports the on-disk HPI version (always VersionV1).
func (r *Reader) Version() uint32 { return common.VersionV1 }

// Close closes the underlying file.
func (r *Reader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// Header returns the archive header.
func (r *Reader) Header() *common.Header { return r.header }

// HeaderKey returns the key byte stored in the header.
func (r *Reader) HeaderKey() uint8 { return uint8(r.header.DecryptKey) }

// EffectiveKey returns the XOR key derived from the header key byte, or 0
// when the archive is not encrypted (header key 0 or 0xFF).
func (r *Reader) EffectiveKey() uint8 { return r.decryptKey }

// Tail returns a copy of the archive's last common.TrailerSize bytes, or nil
// when the file is shorter than that.
func (r *Reader) Tail() []byte { return append([]byte(nil), r.tail...) }

// TrailerValid reports whether the archive ends with the copyright trailer
// TA 3.1c requires ("Copyright ____ Cavedog Entertainment", any year).
func (r *Reader) TrailerValid() bool { return common.ValidTrailer(r.tail) }

// Root returns the root directory entry.
func (r *Reader) Root() *common.Entry { return r.root }

// List returns the full paths of every file record in the archive, in stored
// order. Records a lookup cannot reach (an earlier duplicate name, or a file
// under a directory hidden by a later sibling of the same name) are listed
// too; Find and Open resolve such a path to the record the game reads.
func (r *Reader) List() []string {
	var files []string
	if r.root != nil {
		_ = r.root.Walk(func(e *common.Entry) error {
			if !e.IsDir {
				files = append(files, e.FullPath())
			}
			return nil
		})
	}
	return files
}

// Walk traverses every entry in the archive.
func (r *Reader) Walk(fn func(*common.Entry) error) error {
	if r.root == nil {
		return nil
	}
	return r.root.Walk(fn)
}

// Find locates an entry by path with the game's lookup rules (see
// common.Entry.Find).
func (r *Reader) Find(path string) *common.Entry {
	if r.root == nil {
		return nil
	}
	return r.root.Find(path)
}

// Open opens a file from the archive by path.
func (r *Reader) Open(path string) (io.ReadCloser, error) {
	entry := r.Find(path)
	if entry == nil || entry == r.root {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return r.OpenEntry(entry)
}

// OpenEntry opens a file entry for reading.
func (r *Reader) OpenEntry(entry *common.Entry) (io.ReadCloser, error) {
	if entry.IsDir {
		return nil, errors.New("cannot open directory")
	}
	data, err := r.extractFile(entry)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// ---------------------------------------------------------------------------
// encrypted read helpers
// ---------------------------------------------------------------------------

// readAt reads size bytes at offset and decrypts them with the archive key.
// Every length here derives from on-disk fields, so the range is checked
// against the file before anything is allocated.
func (r *Reader) readAt(offset uint64, size uint64) ([]byte, error) {
	if offset > uint64(r.fileSize) || size > uint64(r.fileSize)-offset {
		return nil, fmt.Errorf("range %d+%d exceeds archive size %d", offset, size, r.fileSize)
	}
	data := make([]byte, size)
	if _, err := r.file.ReadAt(data, int64(offset)); err != nil {
		return nil, err
	}
	common.DecryptBuffer(r.decryptKey, uint8(offset), data)
	return data, nil
}

// ---------------------------------------------------------------------------
// header + directory parsing
// ---------------------------------------------------------------------------

func (r *Reader) readHeader() error {
	info, err := r.file.Stat()
	if err != nil {
		return err
	}
	r.fileSize = info.Size()

	if _, err := r.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	header, err := common.ReadHeader(r.file)
	if err != nil {
		return err
	}
	if header.Version != common.VersionV1 {
		return fmt.Errorf("not a v1 HPI archive: version 0x%X", header.Version)
	}
	r.header = header
	r.decryptKey = common.TransformHeaderKey(uint8(header.DecryptKey))

	if r.fileSize >= common.TrailerSize {
		r.tail = make([]byte, common.TrailerSize)
		if _, err := r.file.ReadAt(r.tail, r.fileSize-common.TrailerSize); err != nil {
			return fmt.Errorf("reading trailer: %w", err)
		}
	}
	return nil
}

// rootOffset returns where the root directory node starts. An offset of 0
// is read as the first byte after the header.
func (r *Reader) rootOffset() (uint32, error) {
	root := r.header.Offset
	if root == 0 {
		return common.HeaderSize, nil
	}
	if root < common.HeaderSize {
		return 0, fmt.Errorf("root directory offset %d overlaps the header", root)
	}
	return root, nil
}

func (r *Reader) readDirectory() error {
	blockSize := r.header.DirectorySize
	if int64(blockSize) > r.fileSize {
		return fmt.Errorf("directory size %d exceeds file size %d", blockSize, r.fileSize)
	}
	if blockSize < common.HeaderSize {
		return fmt.Errorf("directory size %d is smaller than the header", blockSize)
	}
	root, err := r.rootOffset()
	if err != nil {
		return err
	}
	if root > blockSize {
		return fmt.Errorf("directory offset %d past directory end %d", root, blockSize)
	}

	// The block starts at file offset 0: the plaintext header followed by the
	// encrypted directory, which names and records may point anywhere into.
	block := make([]byte, blockSize)
	if _, err := r.file.ReadAt(block, 0); err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}
	common.DecryptBuffer(r.decryptKey, common.HeaderSize, block[common.HeaderSize:])

	r.root = &common.Entry{Name: "", IsDir: true}
	p := dirParser{block: block, visited: make(map[uint32]bool)}
	return p.parse(r.root, root, 0)
}

// dirParser walks the decrypted directory block.
type dirParser struct {
	block   []byte
	visited map[uint32]bool
}

// parse reads the directory node at offset into owner's children.
func (p *dirParser) parse(owner *common.Entry, offset uint32, depth int) error {
	if depth > common.MaxDirectoryDepth {
		return fmt.Errorf("directory nesting exceeds %d levels", common.MaxDirectoryDepth)
	}
	if p.visited[offset] {
		return fmt.Errorf("directory node at offset %d is reached twice (cycle or shared node)", offset)
	}
	p.visited[offset] = true

	block := p.block
	if uint64(offset)+8 > uint64(len(block)) {
		return errors.New("path data out of bounds")
	}
	rawCount := int32(binary.LittleEndian.Uint32(block[offset:]))
	listOffset := binary.LittleEndian.Uint32(block[offset+4:])
	count := uint32(0)
	if rawCount > 0 {
		count = uint32(rawCount)
	}
	if uint64(listOffset) > uint64(len(block)) || uint64(count) > (uint64(len(block))-uint64(listOffset))/common.DirectoryEntrySize {
		return fmt.Errorf("entry list of %d entries at offset %d lies outside the directory", count, listOffset)
	}

	owner.Children = make([]*common.Entry, 0, count)
	for i := uint32(0); i < count; i++ {
		at := listOffset + i*common.DirectoryEntrySize
		nameOffset := binary.LittleEndian.Uint32(block[at:])
		dataOffset := binary.LittleEndian.Uint32(block[at+4:])
		flags := block[at+8]

		name, err := readNullTerminatedString(block, nameOffset)
		if err != nil {
			return fmt.Errorf("failed to read entry name: %w", err)
		}
		entry := &common.Entry{Name: name, IsDir: flags&common.EntryFlagDirectory != 0, Parent: owner}
		if entry.IsDir {
			entry.Offset = dataOffset
		} else {
			if uint64(dataOffset)+common.FileEntrySize > uint64(len(block)) {
				return fmt.Errorf("file data out of bounds for %s", name)
			}
			entry.Offset = binary.LittleEndian.Uint32(block[dataOffset:])
			entry.Size = binary.LittleEndian.Uint32(block[dataOffset+4:])
			entry.CompType = block[dataOffset+8]
		}
		owner.Children = append(owner.Children, entry)
	}

	// Subdirectories are resolved after the whole entry list, depth-first.
	for _, child := range owner.Children {
		if !child.IsDir {
			continue
		}
		nodeOffset := child.Offset
		child.Offset = 0
		if err := p.parse(child, nodeOffset, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// readNullTerminatedString returns the bytes from offset up to the next NUL
// anywhere in the directory block.
func readNullTerminatedString(buffer []byte, offset uint32) (string, error) {
	if uint64(offset) >= uint64(len(buffer)) {
		return "", errors.New("string offset out of bounds")
	}
	end := bytes.IndexByte(buffer[offset:], 0)
	if end < 0 {
		return "", errors.New("no null terminator found")
	}
	return string(buffer[offset : int(offset)+end]), nil
}

// ---------------------------------------------------------------------------
// file extraction
// ---------------------------------------------------------------------------

func (r *Reader) maxEntrySize() int64 {
	if r.opts.MaxEntrySize > 0 {
		return r.opts.MaxEntrySize
	}
	return DefaultMaxEntrySize
}

func (r *Reader) extractFile(entry *common.Entry) ([]byte, error) {
	if int64(entry.Size) > r.maxEntrySize() {
		return nil, fmt.Errorf("%s: size %d exceeds the %d-byte entry limit", entry.Name, entry.Size, r.maxEntrySize())
	}
	if entry.CompType == common.CompressionNone {
		return r.extractStored(entry)
	}
	return r.extractChunked(entry)
}

// extractStored reads a stored entry: size bytes at the record's offset,
// decrypted with the archive key.
func (r *Reader) extractStored(entry *common.Entry) ([]byte, error) {
	data, err := r.readAt(uint64(entry.Offset), uint64(entry.Size))
	if err != nil {
		return nil, fmt.Errorf("%s: truncated stored entry: %w", entry.Name, err)
	}
	return data, nil
}

// chunkCount returns how many 64 KiB chunks a chunked entry of size bytes
// has.
func chunkCount(size uint32) uint32 {
	return uint32((uint64(size) + common.ChunkBlockSize - 1) / common.ChunkBlockSize)
}

// chunkLayout reads a chunked entry's size table and returns each chunk's
// absolute offset and stored size, checking that every chunk lies inside the
// archive.
func (r *Reader) chunkLayout(entry *common.Entry) (offsets []uint64, sizes []uint32, err error) {
	n := chunkCount(entry.Size)
	table, err := r.readAt(uint64(entry.Offset), uint64(n)*4)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: reading chunk size table: %w", entry.Name, err)
	}
	offsets = make([]uint64, n)
	sizes = make([]uint32, n)
	at := uint64(entry.Offset) + uint64(n)*4
	for i := uint32(0); i < n; i++ {
		size := binary.LittleEndian.Uint32(table[i*4:])
		if at+uint64(size) > uint64(r.fileSize) {
			return nil, nil, fmt.Errorf("%s: chunk %d (%d bytes at %d) runs past the archive end", entry.Name, i, size, at)
		}
		offsets[i] = at
		sizes[i] = size
		at += uint64(size)
	}
	return offsets, sizes, nil
}

// extractChunked decodes a chunked entry into its fixed 64 KiB blocks.
func (r *Reader) extractChunked(entry *common.Entry) ([]byte, error) {
	offsets, sizes, err := r.chunkLayout(entry)
	if err != nil {
		return nil, err
	}
	output := make([]byte, entry.Size)
	for i := range offsets {
		chunk, err := r.readAt(offsets[i], uint64(sizes[i]))
		if err != nil {
			return nil, fmt.Errorf("%s: chunk %d: %w", entry.Name, i, err)
		}
		decoded, err := common.DecodeBlock(chunk, r.opts.Strict)
		if err != nil {
			return nil, fmt.Errorf("%s: chunk %d of %d: %w", entry.Name, i, len(offsets), err)
		}
		start := i * common.ChunkBlockSize
		end := start + common.ChunkBlockSize
		if end > len(output) {
			end = len(output)
		}
		if r.opts.Strict && len(decoded) != end-start {
			return nil, fmt.Errorf("%s: chunk %d decodes to %d bytes, its block holds %d", entry.Name, i, len(decoded), end-start)
		}
		copy(output[start:end], decoded)
	}
	return output, nil
}
