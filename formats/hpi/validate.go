package hpi

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/coreprime/kbot-io/formats/hpi/common"
	"github.com/coreprime/kbot-io/formats/hpi/v1"
)

// MountProblem names the first reason TA 3.1c would refuse to mount an
// archive, in the order the game checks them.
type MountProblem int

const (
	// MountOK: the game would mount the archive.
	MountOK MountProblem = iota
	// MountBadMarker: the file is too short or does not start with "HAPI".
	MountBadMarker
	// MountBadVersion: the version is not 1. TA: Kingdoms archives are
	// version 2; TA 3.1c reads version 1 only.
	MountBadVersion
	// MountNoTrailer: the last 36 bytes are not "Copyright ____ Cavedog
	// Entertainment".
	MountNoTrailer
	// MountBadDirectory: the directory does not parse (bounds, names, cycles).
	MountBadDirectory
)

// String returns a short description of the problem.
func (p MountProblem) String() string {
	switch p {
	case MountOK:
		return "ok"
	case MountBadMarker:
		return "not an HPI archive"
	case MountBadVersion:
		return "not a version 1 (Total Annihilation) archive"
	case MountNoTrailer:
		return "missing Cavedog copyright trailer"
	case MountBadDirectory:
		return "unreadable directory"
	default:
		return fmt.Sprintf("MountProblem(%d)", int(p))
	}
}

// Validation reports what TA 3.1c makes of an archive file.
type Validation struct {
	Path string // the file examined
	Size int64  // file size in bytes

	MarkerValid bool   // the file starts with the "HAPI" marker
	Version     uint32 // header version word (VersionV1, VersionV2 or other); 0 when unreadable

	// HeaderKey is the key byte of a v1 header. EffectiveKey is the XOR key
	// derived from it, 0 when the archive is not encrypted (header key 0 or
	// 0xFF). Encrypted reports EffectiveKey != 0. All three are zero for
	// other versions.
	HeaderKey    uint8
	EffectiveKey uint8
	Encrypted    bool

	// Tail holds the file's last 36 bytes (nil for a shorter file).
	// TrailerValid reports whether they are a Cavedog copyright trailer,
	// and TrailerYear holds the trailer's four year characters when it is.
	Tail         []byte
	TrailerValid bool
	TrailerYear  string

	// FileCount is the number of file records in a v1 archive whose
	// directory parsed.
	FileCount int

	// GameMountable reports whether TA 3.1c would mount the archive when it
	// finds it: a v1 archive with a valid trailer and a directory that
	// parses. Problem and Detail say why not.
	GameMountable bool
	Problem       MountProblem
	Detail        string
}

// Validate examines the archive at path the way TA 3.1c does before mounting
// it: marker, version, copyright trailer, then the directory. It reports the
// key derivation and trailer for any version. The error is non-nil only when
// the file cannot be opened or read; a file the game would refuse is
// reported through the Validation.
func Validate(path string) (*Validation, error) {
	a, v, err := OpenForGame(path)
	if a != nil {
		_ = a.Close()
	}
	return v, err
}

// OpenForGame opens the archive at path only if TA 3.1c would mount it: a
// version 1 archive that ends with the Cavedog copyright trailer and whose
// directory parses under the game's rules. It always returns the archive's
// Validation. The Archive is nil when the game would refuse the file; the
// error is non-nil only when the file cannot be opened or read.
func OpenForGame(path string) (Archive, *Validation, error) {
	v := &Validation{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	v.Size = info.Size()

	var head [common.HeaderSize]byte
	n, err := f.ReadAt(head[:], 0)
	if err != nil && err != io.EOF {
		_ = f.Close()
		return nil, nil, err
	}
	if v.Size >= common.TrailerSize {
		v.Tail = make([]byte, common.TrailerSize)
		if _, err := f.ReadAt(v.Tail, v.Size-common.TrailerSize); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		v.TrailerValid = common.ValidTrailer(v.Tail)
		if v.TrailerValid {
			v.TrailerYear = string(v.Tail[common.TrailerYearOffset : common.TrailerYearOffset+common.TrailerYearSize])
		}
	}
	_ = f.Close()

	if n >= 8 {
		v.MarkerValid = binary.LittleEndian.Uint32(head[0:4]) == common.HeaderMarker
		v.Version = binary.LittleEndian.Uint32(head[4:8])
	}
	if n >= 13 && v.Version == common.VersionV1 {
		v.HeaderKey = head[12]
		v.EffectiveKey = common.TransformHeaderKey(v.HeaderKey)
		v.Encrypted = v.EffectiveKey != 0
	}

	switch {
	case !v.MarkerValid:
		v.Problem = MountBadMarker
	case v.Version != common.VersionV1:
		v.Problem = MountBadVersion
		v.Detail = fmt.Sprintf("version 0x%X", v.Version)
		if v.Version == common.VersionV2 {
			v.Detail += " is a TA: Kingdoms archive"
		}
	case !v.TrailerValid:
		v.Problem = MountNoTrailer
		v.Detail = fmt.Sprintf("last %d bytes are %q", len(v.Tail), v.Tail)
	}
	if v.Problem != MountOK {
		return nil, v, nil
	}

	r, err := v1.Open(path)
	if err != nil {
		v.Problem = MountBadDirectory
		v.Detail = err.Error()
		return nil, v, nil
	}
	v.FileCount = len(r.List())
	v.GameMountable = true
	return r, v, nil
}
