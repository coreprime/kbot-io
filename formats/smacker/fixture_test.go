package smacker_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/coreprime/kbot-io/formats/smacker"
)

// smkSpec describes a synthetic Smacker file. Only the container is built:
// the payload bytes are filler, which is all the header reader looks at.
type smkSpec struct {
	sig        string
	width      uint32
	height     uint32
	frames     uint32 // header frame count
	rate       int32
	flags      uint32
	audioSize  [smacker.AudioTrackCount]uint32
	audioRate  [smacker.AudioTrackCount]uint32
	frameSizes []uint32 // table entries as stored (frames, plus the ring frame when flagged)
	frameTypes []byte   // one per table entry; zero-filled when nil
	trees      []byte
	noPayload  bool // omit the frame payloads
	extra      int  // trailing bytes after the payloads
}

// defaultSpec is a small, well-formed SMK2 with three frames and one
// TA-style audio track.
func defaultSpec() smkSpec {
	s := smkSpec{
		sig:        "SMK2",
		width:      64,
		height:     32,
		frames:     3,
		rate:       -3333,
		frameSizes: []uint32{12, 20, 8},
		frameTypes: []byte{0x81, 0x02, 0x04},
		trees:      []byte{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6},
	}
	s.audioRate[0] = 0xD0005622
	s.audioSize[0] = 45572
	return s
}

func (s smkSpec) bytes() []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	w := func(v uint32) { _ = binary.Write(&b, le, v) }
	b.WriteString(s.sig)
	w(s.width)
	w(s.height)
	w(s.frames)
	w(uint32(s.rate))
	w(s.flags)
	for _, v := range s.audioSize {
		w(v)
	}
	w(uint32(len(s.trees))) // TreesSize
	w(0x1111)               // MMapSize
	w(0x2222)               // MClrSize
	w(0x3333)               // FullSize
	w(0x4444)               // TypeSize
	for _, v := range s.audioRate {
		w(v)
	}
	w(0) // unused dword
	for _, v := range s.frameSizes {
		w(v)
	}
	types := s.frameTypes
	if types == nil {
		types = make([]byte, len(s.frameSizes))
	}
	b.Write(types)
	b.Write(s.trees)
	if !s.noPayload {
		for i, v := range s.frameSizes {
			b.Write(bytes.Repeat([]byte{byte(0xF0 + i)}, int(v)))
		}
	}
	b.Write(make([]byte, s.extra))
	return b.Bytes()
}

func (s smkSpec) open(t *testing.T) *smacker.Reader {
	t.Helper()
	data := s.bytes()
	r, err := smacker.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}
