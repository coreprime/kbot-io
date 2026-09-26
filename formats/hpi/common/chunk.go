package common

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// ChunkHeader describes the 19-byte SQSH header that precedes each compressed
// chunk payload.
type ChunkHeader struct {
	Magic            uint32
	Version          uint8
	CompressionType  uint8
	Encoded          uint8
	CompressedSize   uint32
	DecompressedSize uint32
	Checksum         uint32
}

// Errors reported for SQSH chunks that TA 3.1c refuses. Errors returned by
// DecodeBlock wrap one of these, so callers can test them with errors.Is.
var (
	// ErrChunkHeader: the chunk is shorter than a SQSH header or lacks the
	// SQSH marker.
	ErrChunkHeader = errors.New("bad SQSH chunk header")
	// ErrChunkChecksum: the payload's byte sum does not match the header, or
	// the header's packed size runs past the chunk.
	ErrChunkChecksum = errors.New("bad SQSH chunk checksum")
	// ErrChunkType: the SQSH compression type is neither LZ77 (1) nor zlib (2).
	ErrChunkType = errors.New("unsupported SQSH compression type")
	// ErrChunkSize: the chunk decodes to a length other than its header's
	// unpacked size, or its LZ77 stream is malformed.
	ErrChunkSize = errors.New("bad SQSH unpacked size")
	// ErrChunkParams: the header's unpacked size exceeds ChunkBlockSize.
	ErrChunkParams = errors.New("SQSH unpacked size exceeds the 64 KiB block")
)

// maxLZ77Expansion bounds how many output bytes one input byte of an LZ77
// stream can produce: a tag byte plus eight two-byte back references of 17
// bytes each is 136 bytes from 17 input bytes.
const maxLZ77Expansion = 8

// maxZlibExpansion bounds how many output bytes one input byte of a deflate
// stream can produce (the format's limit is about 1032:1).
const maxZlibExpansion = 1040

// DecodeBlock decodes one SQSH chunk of a Total Annihilation (v1) chunked
// entry with the rules TA 3.1c applies. block is the chunk as the entry's
// chunk-size table delimits it, already decrypted with the archive key.
//
// The rules, in the order they are checked:
//
//   - the block must hold a 19-byte header starting with the SQSH marker; the
//     version byte is ignored;
//   - the compression type must be 1 (LZ77) or 2 (zlib); 0 (stored) and 3
//     are refused like every other value;
//   - the packed size must fit inside the block, and the byte sum of the
//     packed payload (as stored, before the add/XOR transform is undone) must
//     match the header's checksum;
//   - the unpacked size must not exceed ChunkBlockSize;
//   - an LZ77 stream must end with its terminator and decode to exactly the
//     unpacked size;
//   - a zlib stream that ends cleanly must decode to exactly the unpacked
//     size. The game keeps the unpacked size, and the bytes decoded so far
//     followed by zeros, when the stream is corrupt, fails its Adler-32 check,
//     is cut short or runs longer; with strict set those cases are errors
//     instead.
//
// The returned slice is exactly the header's unpacked size.
func DecodeBlock(block []byte, strict bool) ([]byte, error) {
	if len(block) < SQSHHeaderSize || binary.LittleEndian.Uint32(block[:4]) != ChunkMarker {
		return nil, ErrChunkHeader
	}
	compType := block[5]
	encoded := block[6]
	packed := binary.LittleEndian.Uint32(block[7:11])
	unpacked := binary.LittleEndian.Uint32(block[11:15])
	checksum := binary.LittleEndian.Uint32(block[15:19])

	if compType != CompressionLZ77 && compType != CompressionZLib && compType != CompressionNone && compType != 3 {
		return nil, fmt.Errorf("%w: %d", ErrChunkType, compType)
	}
	if uint64(packed) > uint64(len(block)-SQSHHeaderSize) {
		return nil, fmt.Errorf("%w: packed size %d exceeds the %d bytes after the header", ErrChunkChecksum, packed, len(block)-SQSHHeaderSize)
	}
	payload := make([]byte, packed)
	copy(payload, block[SQSHHeaderSize:])
	if sum := Checksum(payload); sum != checksum {
		return nil, fmt.Errorf("%w: expected 0x%X, got 0x%X", ErrChunkChecksum, checksum, sum)
	}
	if encoded != 0 {
		DecodeChunkBuffer(payload)
	}
	if unpacked > ChunkBlockSize {
		return nil, fmt.Errorf("%w: %d", ErrChunkParams, unpacked)
	}

	switch compType {
	case CompressionLZ77:
		out, err := lz77Decode(payload, ChunkBlockSize)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChunkSize, err)
		}
		if len(out) != int(unpacked) {
			return nil, fmt.Errorf("%w: header says %d, LZ77 decoded %d", ErrChunkSize, unpacked, len(out))
		}
		return out, nil
	case CompressionZLib:
		return inflateBlock(payload, int(unpacked), strict)
	default:
		// Types 0 and 3 pass the type check but no decoder accepts them.
		return nil, fmt.Errorf("%w: SQSH type %d inside a chunked entry", ErrChunkSize, compType)
	}
}

// inflateBlock inflates a zlib chunk into exactly want bytes, following the
// length contract of the zlib inflate the game uses: a stream that ends
// cleanly reports how much it produced, which must equal want; any other
// outcome keeps want bytes (decoded bytes followed by zeros) unless strict is
// set.
func inflateBlock(payload []byte, want int, strict bool) ([]byte, error) {
	out := make([]byte, want)
	zr, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		if strict {
			return nil, fmt.Errorf("zlib header: %w", err)
		}
		return out, nil
	}
	defer func() { _ = zr.Close() }()

	n := 0
	for n < want {
		m, rerr := zr.Read(out[n:])
		n += m
		if rerr == io.EOF {
			if n != want {
				return nil, fmt.Errorf("%w: header says %d, zlib stream ended after %d", ErrChunkSize, want, n)
			}
			return out, nil
		}
		if rerr != nil {
			if strict {
				return nil, fmt.Errorf("zlib decode: %w", rerr)
			}
			return out, nil
		}
	}
	if strict {
		var extra [1]byte
		m, rerr := zr.Read(extra[:])
		if m != 0 {
			return nil, fmt.Errorf("zlib stream runs past the %d bytes the header states", want)
		}
		if rerr != nil && rerr != io.EOF {
			return nil, fmt.Errorf("zlib decode: %w", rerr)
		}
	}
	return out, nil
}

// DecodeChunk parses a single SQSH chunk whose header starts at the front of
// buf and returns the decompressed payload. It is the decoder for TA: Kingdoms
// (v2) archives, whose files and directory blocks are single chunks of any
// size; v1 chunked entries use DecodeBlock. The bytes in buf are expected to be
// already decrypted. The verified checksum is computed on the stored payload
// before the optional add/XOR transform is reversed.
//
// Types 0 (stored), 1 (LZ77) and 2 (zlib) are accepted. The output must equal
// the header's decompressed size, an LZ77 stream must end with its terminator,
// and the output is never allowed to grow past what the payload can encode,
// so a forged size cannot force a large allocation.
func DecodeChunk(buf []byte) ([]byte, error) {
	if len(buf) < SQSHHeaderSize {
		return nil, fmt.Errorf("chunk too small: %d bytes", len(buf))
	}
	if binary.LittleEndian.Uint32(buf[:4]) != ChunkMarker {
		return nil, fmt.Errorf("invalid SQSH marker: 0x%X", binary.LittleEndian.Uint32(buf[:4]))
	}
	compType := buf[5]
	encoded := buf[6]
	compSize := binary.LittleEndian.Uint32(buf[7:11])
	decompSize := binary.LittleEndian.Uint32(buf[11:15])
	checksum := binary.LittleEndian.Uint32(buf[15:19])

	if uint64(SQSHHeaderSize)+uint64(compSize) > uint64(len(buf)) {
		return nil, fmt.Errorf("compressed payload %d exceeds buffer (%d available)", compSize, len(buf)-SQSHHeaderSize)
	}
	payload := make([]byte, compSize)
	copy(payload, buf[SQSHHeaderSize:SQSHHeaderSize+compSize])

	if sum := Checksum(payload); sum != checksum {
		return nil, fmt.Errorf("SQSH checksum mismatch: expected 0x%X, got 0x%X", checksum, sum)
	}
	if encoded != 0 {
		DecodeChunkBuffer(payload)
	}

	return decompressChunkPayload(compType, payload, decompSize)
}

// decompressChunkPayload applies the chunk's compression algorithm to an
// already-decoded payload, validating the decompressed length against the
// header's stated size.
func decompressChunkPayload(compType uint8, payload []byte, decompSize uint32) ([]byte, error) {
	if uint64(decompSize) > math.MaxInt {
		return nil, fmt.Errorf("chunk size %d exceeds the addressable range", decompSize)
	}
	want := int(decompSize)
	switch compType {
	case CompressionNone:
		if len(payload) != want {
			return nil, fmt.Errorf("stored chunk size mismatch: header says %d, payload has %d", decompSize, len(payload))
		}
		return payload, nil
	case CompressionZLib:
		if uint64(want) > uint64(len(payload))*maxZlibExpansion+64 {
			return nil, fmt.Errorf("zlib chunk size %d cannot come from a %d-byte payload", decompSize, len(payload))
		}
		zr, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("zlib reader: %w", err)
		}
		defer func() { _ = zr.Close() }()
		var out bytes.Buffer
		out.Grow(want)
		if _, err := io.Copy(&out, io.LimitReader(zr, int64(want)+1)); err != nil {
			return nil, fmt.Errorf("zlib decode: %w", err)
		}
		if out.Len() != want {
			return nil, fmt.Errorf("zlib chunk size mismatch: header says %d, decoded %d", decompSize, out.Len())
		}
		return out.Bytes(), nil
	case CompressionLZ77:
		if uint64(want) > uint64(len(payload))*maxLZ77Expansion+64 {
			return nil, fmt.Errorf("LZ77 chunk size %d cannot come from a %d-byte payload", decompSize, len(payload))
		}
		return DecompressLZ77(payload, want)
	default:
		return nil, fmt.Errorf("unknown SQSH compression type: %d", compType)
	}
}
