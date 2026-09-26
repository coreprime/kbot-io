package v1

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

const chunkMaxDecomp = common.ChunkBlockSize

// ErrInvalidTrailer is returned by Close when the trailer is not a copyright
// trailer TA 3.1c accepts and AllowNonGameTrailer is not set.
var ErrInvalidTrailer = errors.New(`HPI trailer must be the 36 bytes "Copyright ____ Cavedog Entertainment"; TA 3.1c does not mount archives without it`)

// WriterEntry holds a file's metadata and payload for writing into an archive.
type WriterEntry struct {
	Path string
	Data []byte

	// When IsRawPassthrough is set the writer stores RawChunks verbatim
	// instead of compressing Data. This is used for byte-perfect round-trips
	// where the original compressed representation must be preserved.
	RawChunks        []byte
	DecompSize       uint32
	CompType         uint8
	IsRawPassthrough bool
}

// Writer builds a Total Annihilation (v1) HPI archive.
//
// Paths are '/'- or '\'-separated. Directories are merged ignoring ASCII
// letter case, keeping the first spelling added, as the game looks names up
// case-insensitively. Adding a second file whose path differs only in letter
// case keeps both records; the game reads the one added last. Warnings lists
// such collisions.
//
// File data is written in the order files were added, and every record points
// at its own data.
type Writer struct {
	file    *os.File
	entries []WriterEntry
	trailer []byte
	err     error // first error from a call without an error result

	CompressionLevel int // zlib level (0–9); 0 means default

	// CompressionMethod selects how AddFile and AddFileFromBytes store data:
	// common.CompressionNone (0) stores it uncompressed, common.CompressionLZ77
	// (1, the CreateWriter default) and common.CompressionZLib (2) split it
	// into 64 KiB chunks compressed with that algorithm. Other values are
	// refused. A chunked entry never contains a stored (type 0) SQSH chunk,
	// which the game refuses.
	CompressionMethod uint8

	// HeaderKey is the raw HeaderKey value stored in the HPI header. The
	// reader transforms it into the per-byte XOR key used for the directory
	// and chunk regions. A value of 0 or 0xFF disables encryption. Defaults
	// to common.DefaultHeaderKey when the writer is created via CreateWriter.
	HeaderKey uint8

	// ChunkEncoded controls whether each SQSH chunk's compressed payload is
	// run through the per-position add/XOR transform. Defaults to true,
	// matching every chunk shipped in retail TA archives.
	ChunkEncoded bool

	// AllowNonGameTrailer lets Close write a trailer set with SetTrailer that
	// is not a valid Cavedog copyright trailer, including none at all. TA 3.1c
	// does not mount such archives; kbot-io's reader still opens them.
	AllowNonGameTrailer bool
}

// CreateWriter creates a new v1 HPI archive at the given path. The writer is
// initialised with the Total Annihilation retail defaults: HeaderKey 0xBF,
// LZ77 compression, encoded chunks, and the Cavedog copyright trailer.
// Override any of these fields (or call SetTrailer) before adding files to
// change the produced archive.
func CreateWriter(path string) (*Writer, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Writer{
		file:              file,
		HeaderKey:         common.DefaultHeaderKey,
		CompressionMethod: common.CompressionLZ77,
		ChunkEncoded:      true,
		trailer:           []byte(common.DefaultTrailer),
	}, nil
}

// SetTrailer sets the bytes appended after the file data section. TA 3.1c
// mounts an archive only when its last 36 bytes read "Copyright ____ Cavedog
// Entertainment" (any four year characters; see common.Trailer), so Close
// fails with ErrInvalidTrailer for any other value unless AllowNonGameTrailer
// is set.
func (w *Writer) SetTrailer(data []byte) {
	w.trailer = append([]byte(nil), data...)
}

// AddFile reads a file from disk and adds it to the archive.
func (w *Writer) AddFile(archivePath, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}
	return w.AddFileFromBytes(archivePath, data)
}

// AddFileFromBytes adds a file from an in-memory byte slice. The compression
// method used is determined by the Writer's CompressionMethod field. The path
// must pass common.CleanArchivePath: no empty, "." or ".." segments and no
// name longer than common.MaxNameLength bytes.
func (w *Writer) AddFileFromBytes(archivePath string, data []byte) error {
	method := w.CompressionMethod
	if method > common.CompressionZLib {
		return fmt.Errorf("unsupported compression method %d (use 0 none, 1 LZ77 or 2 zlib)", method)
	}
	p, err := common.CleanArchivePath(archivePath)
	if err != nil {
		return err
	}
	w.entries = append(w.entries, WriterEntry{
		Path:       p,
		Data:       data,
		DecompSize: uint32(len(data)),
		CompType:   method,
	})
	return nil
}

// AddRawEntry adds a file entry whose data will be written verbatim, as
// returned by Reader.ReadRawFileData. compType is the file record's
// compression byte: 0 for stored data, any other value for a chunk-size table
// followed by chunks. Used for lossless round-trips. An invalid path makes
// Close fail.
func (w *Writer) AddRawEntry(archivePath string, rawChunks []byte, decompSize uint32, compType uint8) {
	p, err := common.CleanArchivePath(archivePath)
	if err != nil {
		if w.err == nil {
			w.err = err
		}
		return
	}
	w.entries = append(w.entries, WriterEntry{
		Path:             p,
		RawChunks:        rawChunks,
		DecompSize:       decompSize,
		CompType:         compType,
		IsRawPassthrough: true,
	})
}

// AddDirectory recursively adds every file under dirPath, rooted at
// archivePath inside the archive.
func (w *Writer) AddDirectory(archivePath, dirPath string) error {
	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		ap := filepath.ToSlash(rel)
		if archivePath != "" {
			ap = archivePath + "/" + ap
		}
		return w.AddFile(ap, path)
	})
}

// Warnings describes paths added so far that collide when names are compared
// the way the game compares them (ignoring ASCII letter case): two files with
// the same path, where the game reads only the one added last, and a file
// that shares its name with a directory, where the one added last hides the
// other.
func (w *Writer) Warnings() []string {
	_, warnings := buildTree(w.entries)
	return warnings
}

// Close finalises the archive and closes the underlying file.
func (w *Writer) Close() error {
	if err := w.writeArchive(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

// ---------------------------------------------------------------------------
// internal: directory tree used during serialization
// ---------------------------------------------------------------------------

type dirNode struct {
	name     string
	children []*dirNode    // in insertion order
	files    []*fileNode   //
	ordered  []interface{} // interleaved *dirNode / *fileNode in insertion order
}

type fileNode struct {
	name       string
	entryIndex int // index into Writer.entries
}

// findOrCreateChild returns the child directory whose name equals name
// ignoring ASCII letter case, creating it with this spelling if none exists.
func (d *dirNode) findOrCreateChild(name string) *dirNode {
	for _, c := range d.children {
		if common.EqualFoldASCII(c.name, name) {
			return c
		}
	}
	child := &dirNode{name: name}
	d.children = append(d.children, child)
	d.ordered = append(d.ordered, child)
	return child
}

func (d *dirNode) addFile(name string, idx int) {
	fn := &fileNode{name: name, entryIndex: idx}
	d.files = append(d.files, fn)
	d.ordered = append(d.ordered, fn)
}

func (d *dirNode) hasFile(name string) bool {
	for _, f := range d.files {
		if common.EqualFoldASCII(f.name, name) {
			return true
		}
	}
	return false
}

func (d *dirNode) hasDir(name string) bool {
	for _, c := range d.children {
		if common.EqualFoldASCII(c.name, name) {
			return true
		}
	}
	return false
}

// buildTree arranges the entries into directories and reports name
// collisions.
func buildTree(entries []WriterEntry) (*dirNode, []string) {
	root := &dirNode{}
	var warnings []string
	for i, e := range entries {
		parts := strings.Split(e.Path, "/")
		cur := root
		for j, p := range parts[:len(parts)-1] {
			if cur.hasFile(p) {
				warnings = append(warnings, fmt.Sprintf("%s: directory %q shares its name with a file", e.Path, strings.Join(parts[:j+1], "/")))
			}
			cur = cur.findOrCreateChild(p)
		}
		name := parts[len(parts)-1]
		switch {
		case cur.hasFile(name):
			warnings = append(warnings, fmt.Sprintf("%s: path added more than once (ignoring letter case); the game reads the last copy", e.Path))
		case cur.hasDir(name):
			warnings = append(warnings, fmt.Sprintf("%s: file shares its name with a directory", e.Path))
		}
		cur.addFile(name, i)
	}
	return root, warnings
}

// ---------------------------------------------------------------------------
// internal: two-pass directory serialization
//
// Pass 1 – compute sizes so we know absolute offsets.
// Pass 2 – emit bytes.
//
// Layout of a directory node D with N children (dirs + files):
//
//	DirNodeHeader  [8 bytes]  numEntries | entryListOffset
//	EntryList      [N×9 bytes]
//	For each child in insertion order:
//	  NullTerminatedName
//	  if file:  FileDataRecord [9 bytes]
//	  if dir:   [child subtree recursively]
// ---------------------------------------------------------------------------

// dirSize returns the total byte size of the serialized directory subtree
// rooted at d (not including d's own DirNodeHeader, which is written by the
// parent's child-loop).
func dirSize(d *dirNode) int {
	n := len(d.ordered)
	size := 8 + n*9 // DirNodeHeader(8) + EntryList(N*9)
	for _, item := range d.ordered {
		switch v := item.(type) {
		case *fileNode:
			size += len(v.name) + 1 + 9 // name\0 + FileDataRecord
		case *dirNode:
			size += len(v.name) + 1 + dirSize(v) // name\0 + subtree
		}
	}
	return size
}

// serializeDir writes the directory subtree into buf starting at buf[base].
// absBase is the absolute file offset that corresponds to buf[base]; all
// pointers written into the buffer use absolute file offsets. fileOffsets
// holds each entry's data offset, assigned before serialization.
func serializeDir(buf []byte, base int, absBase int, d *dirNode, fileOffsets []uint32, entries []WriterEntry) {
	n := uint32(len(d.ordered))
	entryListOff := absBase + 8

	binary.LittleEndian.PutUint32(buf[base:], n)
	binary.LittleEndian.PutUint32(buf[base+4:], uint32(entryListOff))

	payloadBuf := base + 8 + int(n)*9
	payloadAbs := absBase + 8 + int(n)*9

	for i, item := range d.ordered {
		entryBuf := (base + 8) + i*9

		switch v := item.(type) {
		case *fileNode:
			nameAbs := payloadAbs
			copy(buf[payloadBuf:], v.name)
			buf[payloadBuf+len(v.name)] = 0
			payloadBuf += len(v.name) + 1
			payloadAbs += len(v.name) + 1

			dataRecAbs := payloadAbs
			dataRecBuf := payloadBuf
			e := entries[v.entryIndex]

			binary.LittleEndian.PutUint32(buf[dataRecBuf:], fileOffsets[v.entryIndex])
			binary.LittleEndian.PutUint32(buf[dataRecBuf+4:], e.DecompSize)
			buf[dataRecBuf+8] = e.CompType
			payloadBuf += 9
			payloadAbs += 9

			binary.LittleEndian.PutUint32(buf[entryBuf:], uint32(nameAbs))
			binary.LittleEndian.PutUint32(buf[entryBuf+4:], uint32(dataRecAbs))
			buf[entryBuf+8] = common.EntryTypeFile

		case *dirNode:
			nameAbs := payloadAbs
			copy(buf[payloadBuf:], v.name)
			buf[payloadBuf+len(v.name)] = 0
			payloadBuf += len(v.name) + 1
			payloadAbs += len(v.name) + 1

			childBuf := payloadBuf
			childAbs := payloadAbs

			binary.LittleEndian.PutUint32(buf[entryBuf:], uint32(nameAbs))
			binary.LittleEndian.PutUint32(buf[entryBuf+4:], uint32(childAbs))
			buf[entryBuf+8] = common.EntryTypeDirectory

			serializeDir(buf, childBuf, childAbs, v, fileOffsets, entries)
			subtreeSize := dirSize(v)
			payloadBuf = childBuf + subtreeSize
			payloadAbs = childAbs + subtreeSize
		}
	}
}

// buildChunks compresses data into the HPI chunk format (size table + SQSH
// chunk headers + payloads). compType selects the algorithm (LZ77 or zlib);
// stored entries never go through here, so no chunk is ever SQSH type 0.
// level is the zlib compression level; values ≤0 use zlib.DefaultCompression.
// When chunkEncoded is true the compressed bytes of each chunk are run through
// the chunk transform before the checksum is computed, and the chunk header's
// "encoded" byte is set so the reader applies the inverse pass.
//
// An empty file has no chunks: the chunk count is ceil(size/65536), which is
// what readers derive from the file record.
func buildChunks(data []byte, compType uint8, level int, chunkEncoded bool) []byte {
	numChunks := (len(data) + chunkMaxDecomp - 1) / chunkMaxDecomp

	type chunk struct {
		compressed []byte
		decompSize uint32
	}
	chunks := make([]chunk, numChunks)
	for i := range chunks {
		lo := i * chunkMaxDecomp
		hi := lo + chunkMaxDecomp
		if hi > len(data) {
			hi = len(data)
		}
		block := data[lo:hi]

		var compressed []byte
		switch compType {
		case common.CompressionLZ77:
			compressed = common.CompressLZ77(block)
		default:
			var zbuf bytes.Buffer
			zlibLevel := level
			if zlibLevel <= 0 {
				zlibLevel = zlib.DefaultCompression
			}
			zw, _ := zlib.NewWriterLevel(&zbuf, zlibLevel)
			_, _ = zw.Write(block)
			_ = zw.Close()
			compressed = zbuf.Bytes()
		}
		if chunkEncoded {
			common.EncodeChunkBuffer(compressed)
		}
		chunks[i] = chunk{compressed: compressed, decompSize: uint32(len(block))}
	}

	encodedByte := byte(0)
	if chunkEncoded {
		encodedByte = 1
	}

	total := numChunks * 4
	for _, c := range chunks {
		total += common.SQSHHeaderSize + len(c.compressed)
	}

	out := make([]byte, total)
	pos := 0

	for _, c := range chunks {
		chunkTotal := uint32(common.SQSHHeaderSize + len(c.compressed))
		binary.LittleEndian.PutUint32(out[pos:], chunkTotal)
		pos += 4
	}

	for _, c := range chunks {
		binary.LittleEndian.PutUint32(out[pos:], common.ChunkMarker)
		out[pos+4] = 2 // version
		out[pos+5] = compType
		out[pos+6] = encodedByte
		binary.LittleEndian.PutUint32(out[pos+7:], uint32(len(c.compressed)))
		binary.LittleEndian.PutUint32(out[pos+11:], c.decompSize)
		binary.LittleEndian.PutUint32(out[pos+15:], common.Checksum(c.compressed))
		pos += common.SQSHHeaderSize
		copy(out[pos:], c.compressed)
		pos += len(c.compressed)
	}

	return out
}

// ---------------------------------------------------------------------------
// writeArchive lays out: Header | DirSection | FileData | Trailer
// ---------------------------------------------------------------------------

func (w *Writer) writeArchive() error {
	if w.err != nil {
		return w.err
	}
	if len(w.entries) == 0 {
		return fmt.Errorf("no entries to write")
	}
	if !w.AllowNonGameTrailer && !common.ValidTrailer(w.trailer) {
		return ErrInvalidTrailer
	}

	blobs := make([][]byte, len(w.entries))
	for i, e := range w.entries {
		switch {
		case e.IsRawPassthrough:
			blobs[i] = e.RawChunks
		case e.CompType == common.CompressionNone:
			blobs[i] = e.Data
		default:
			blobs[i] = buildChunks(e.Data, e.CompType, w.CompressionLevel, w.ChunkEncoded)
		}
	}

	tree, _ := buildTree(w.entries)
	dirSectionSize := dirSize(tree)
	fileDataStart := uint64(common.HeaderSize + dirSectionSize)

	// Data is laid out in the order the files were added, and each record
	// points at its own data.
	fileOffsets := make([]uint32, len(w.entries))
	next := fileDataStart
	for i, blob := range blobs {
		if next > math.MaxUint32 {
			return fmt.Errorf("archive exceeds 4 GiB at %s", w.entries[i].Path)
		}
		fileOffsets[i] = uint32(next)
		next += uint64(len(blob))
	}
	if next+uint64(len(w.trailer)) > math.MaxUint32 {
		return fmt.Errorf("archive exceeds 4 GiB")
	}

	dirBuf := make([]byte, dirSectionSize)
	serializeDir(dirBuf, 0, common.HeaderSize, tree, fileOffsets, w.entries)

	hdr := common.Header{
		Marker:        common.HeaderMarker,
		Version:       common.VersionV1,
		DirectorySize: uint32(fileDataStart),
		DecryptKey:    uint32(w.HeaderKey),
		Offset:        uint32(common.HeaderSize),
	}
	if err := hdr.WriteHeader(w.file); err != nil {
		return err
	}

	xorKey := common.TransformHeaderKey(w.HeaderKey)
	offset := int64(common.HeaderSize)

	common.EncryptInPlace(xorKey, offset, dirBuf)
	if _, err := w.file.Write(dirBuf); err != nil {
		return fmt.Errorf("writing directory: %w", err)
	}
	offset += int64(len(dirBuf))

	for i, blob := range blobs {
		if xorKey != 0 {
			enc := make([]byte, len(blob))
			copy(enc, blob)
			common.EncryptInPlace(xorKey, offset, enc)
			blob = enc
		}
		if _, err := w.file.Write(blob); err != nil {
			return fmt.Errorf("writing file data for %s: %w", w.entries[i].Path, err)
		}
		offset += int64(len(blob))
	}

	if len(w.trailer) > 0 {
		if _, err := w.file.Write(w.trailer); err != nil {
			return fmt.Errorf("writing trailer: %w", err)
		}
	}

	return nil
}
