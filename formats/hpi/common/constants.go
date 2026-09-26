package common

// Format constants shared by every HPI version. Total Annihilation and
// TA: Kingdoms wrap their data in the same outer container; only the
// directory layout and chunk encryption differ between versions.

const (
	// HeaderMarker is the magic number identifying HPI files ("HAPI").
	HeaderMarker = 0x49504148

	// ChunkMarker is the magic number for compressed chunks ("SQSH").
	ChunkMarker = 0x48535153

	// HeaderSize is the size of the HPI v1 header in bytes.
	HeaderSize = 20

	// DirectoryEntrySize is the size of a v1 directory entry.
	DirectoryEntrySize = 9

	// FileEntrySize is the size of a v1 file entry.
	FileEntrySize = 9

	// ChunkHeaderSize is the size of a v1 chunk header.
	ChunkHeaderSize = 9

	// SQSHHeaderSize is the on-disk size of a SQSH chunk header
	// (uint32 marker + 3 uint8s + 3 uint32s = 19 bytes, packed).
	SQSHHeaderSize = 19

	// ChunkBlockSize is the number of decoded bytes each chunk of a v1
	// chunked entry covers. Chunk i fills bytes [i*ChunkBlockSize,
	// (i+1)*ChunkBlockSize) of the entry, and no chunk may decode to more.
	ChunkBlockSize = 65536

	// TrailerSize is the length of the copyright trailer that ends every
	// archive the game mounts.
	TrailerSize = 36

	// TrailerYearOffset and TrailerYearSize locate the four year characters
	// inside the trailer. The game does not compare them.
	TrailerYearOffset = 10
	TrailerYearSize   = 4

	// MaxNameLength is the longest entry name, in bytes, the writers accept.
	// It is the longest file name Windows file systems hold, so every entry
	// of a written archive can also exist as a loose file in the game
	// directory. Readers accept any length up to the end of the directory.
	MaxNameLength = 255

	// MaxDirectoryDepth bounds directory nesting when reading. Retail
	// archives nest a few levels deep; the bound only stops a malformed
	// archive from exhausting the stack.
	MaxDirectoryDepth = 128
)

// HPI archive versions.
const (
	VersionV1 uint32 = 0x00010000 // Total Annihilation
	VersionV2 uint32 = 0x00020000 // TA: Kingdoms
)

// Compression types stored in chunk headers and v1 file records.
//
// In a v1 file record, 0 marks a stored entry and any other value a chunked
// entry; the algorithm of each chunk is the SQSH type in its own header. In a
// SQSH header, 1 is LZ77 and 2 is zlib; TA 3.1c refuses every other type,
// including 0, inside a chunked entry.
const (
	CompressionNone = 0 // No compression
	CompressionLZ77 = 1 // LZ77 compression
	CompressionZLib = 2 // ZLib compression
)

// Entry types used by v1 directory records.
//
// Only bit 0 of the record's flag byte is meaningful: it marks a directory.
// The game ignores the other bits, so a flag of 0x02 is a file.
const (
	EntryTypeDirectory = 1
	EntryTypeFile      = 0

	// EntryFlagDirectory is the flag bit that marks a directory record.
	EntryFlagDirectory = 0x01
)

// DecryptKey XORs every byte with 0xFF for encrypted HPIs.
const DecryptKey = ^byte(0)

// DefaultHeaderKey is the HeaderKey value Total Annihilation writes into the
// HPI header for the main game archives shipped on disk (totala1/2/4.hpi).
// A value of 0 or 0xFF disables encryption entirely.
const DefaultHeaderKey uint8 = 0xBF

// DefaultTrailer is the 36-byte ASCII signature written at the end of every
// retail TA HPI archive. TA 3.1c refuses to mount an archive whose last 36
// bytes are not "Copyright " followed by any four characters and
// " Cavedog Entertainment", so the v1 writer requires a trailer of this form.
const DefaultTrailer = "Copyright 1997 Cavedog Entertainment"
