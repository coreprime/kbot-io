package v1

import (
	"fmt"
	"io"

	"github.com/coreprime/kbot-io/formats/hpi/common"
)

// ReadRawFileData returns the raw on-disk bytes of a file entry, decrypted
// with the archive key: for a chunked entry the chunk-size table followed by
// every chunk as the table delimits it, and for a stored entry (compression
// byte 0) the entry's size bytes. The result can be written back verbatim
// into a new archive via AddRawEntry with the same size and compression byte.
func (r *Reader) ReadRawFileData(entry *common.Entry) ([]byte, error) {
	if entry.IsDir {
		return nil, fmt.Errorf("cannot read raw data for a directory")
	}
	start, end, err := r.rawExtent(entry)
	if err != nil {
		return nil, err
	}
	return r.readAt(start, end-start)
}

// rawExtent returns the byte range [start, end) an entry's data occupies.
func (r *Reader) rawExtent(entry *common.Entry) (start, end uint64, err error) {
	start = uint64(entry.Offset)
	if entry.CompType == common.CompressionNone {
		end = start + uint64(entry.Size)
		if end > uint64(r.fileSize) {
			return 0, 0, fmt.Errorf("%s: stored entry runs past the archive end", entry.Name)
		}
		return start, end, nil
	}
	offsets, sizes, err := r.chunkLayout(entry)
	if err != nil {
		return 0, 0, err
	}
	end = start + uint64(len(offsets))*4
	if n := len(offsets); n > 0 {
		end = offsets[n-1] + uint64(sizes[n-1])
	}
	return start, end, nil
}

// ReadTrailer returns any bytes that follow the last file's data up to the
// end of the archive, unmodified. Returns nil if there is no trailing data.
// Entries whose data cannot be located are ignored.
func (r *Reader) ReadTrailer() ([]byte, error) {
	var maxEnd uint64
	if r.root != nil {
		_ = r.root.Walk(func(e *common.Entry) error {
			if e.IsDir {
				return nil
			}
			if _, end, err := r.rawExtent(e); err == nil && end > maxEnd {
				maxEnd = end
			}
			return nil
		})
	}

	fileSize, err := r.file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if dirEnd := uint64(r.header.DirectorySize); dirEnd > maxEnd {
		maxEnd = dirEnd
	}
	if maxEnd >= uint64(fileSize) {
		return nil, nil
	}

	trailer := make([]byte, uint64(fileSize)-maxEnd)
	if _, err := r.file.ReadAt(trailer, int64(maxEnd)); err != nil {
		return nil, err
	}
	return trailer, nil
}
