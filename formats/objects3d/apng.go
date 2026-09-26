package objects3d

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"math"
)

// encodeAPNG writes a sequence of equally-sized truecolor RGBA frames as an
// animated PNG. Each frame is delayed delayNum/delayDen seconds. Frame image
// data is produced by the stdlib PNG encoder (so compression + filtering match
// a normal PNG); this only wraps it in the APNG chunk structure (acTL / fcTL /
// fdAT). With one frame it degrades to a plain PNG.
//
// Every frame of an APNG shares the one IHDR, so all frames are encoded with
// the same colour type: RGB when every frame is opaque, RGBA otherwise.
func encodeAPNG(frames []*image.RGBA, delayNum, delayDen uint16) ([]byte, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames")
	}
	if len(frames) == 1 {
		var buf bytes.Buffer
		if err := png.Encode(&buf, frames[0]); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	W := frames[0].Bounds().Dx()
	H := frames[0].Bounds().Dy()
	for i, fr := range frames[1:] {
		if fr.Bounds().Dx() != W || fr.Bounds().Dy() != H {
			return nil, fmt.Errorf("frame %d is %dx%d, frame 0 is %dx%d", i+1, fr.Bounds().Dx(), fr.Bounds().Dy(), W, H)
		}
	}

	chunks := make([]pngChunks, len(frames))
	sameHeader := true
	for i, fr := range frames {
		pc, err := encodePNGChunks(fr)
		if err != nil {
			return nil, err
		}
		chunks[i] = pc
		sameHeader = sameHeader && bytes.Equal(pc.ihdr, chunks[0].ihdr)
	}
	if !sameHeader {
		// Some frames are opaque and some are not: re-encode all as RGBA.
		for i, fr := range frames {
			pc, err := encodePNGChunks(translucentRGBA{fr})
			if err != nil {
				return nil, err
			}
			chunks[i] = pc
		}
	}

	var out bytes.Buffer
	out.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10}) // PNG signature

	// Frame 0 supplies the IHDR + the default-image IDAT.
	writeAPNGChunk(&out, "IHDR", chunks[0].ihdr)

	actl := make([]byte, 8)
	binary.BigEndian.PutUint32(actl[0:], uint32(len(frames)))
	binary.BigEndian.PutUint32(actl[4:], 0) // play count: infinite
	writeAPNGChunk(&out, "acTL", actl)

	seq := uint32(0)
	fctl := func(s uint32) {
		b := make([]byte, 26)
		binary.BigEndian.PutUint32(b[0:], s)
		binary.BigEndian.PutUint32(b[4:], uint32(W))
		binary.BigEndian.PutUint32(b[8:], uint32(H))
		// x/y offset: 0 (bytes 12..19)
		binary.BigEndian.PutUint16(b[20:], delayNum)
		binary.BigEndian.PutUint16(b[22:], delayDen)
		b[24] = 1 // dispose_op = APNG_DISPOSE_OP_BACKGROUND
		b[25] = 0 // blend_op   = APNG_BLEND_OP_SOURCE
		writeAPNGChunk(&out, "fcTL", b)
	}

	fctl(seq)
	seq++
	writeAPNGChunk(&out, "IDAT", chunks[0].idat)

	for _, pc := range chunks[1:] {
		fctl(seq)
		seq++
		fdat := make([]byte, 4+len(pc.idat))
		binary.BigEndian.PutUint32(fdat[0:], seq)
		seq++
		copy(fdat[4:], pc.idat)
		writeAPNGChunk(&out, "fdAT", fdat)
	}

	writeAPNGChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}

// translucentRGBA is an RGBA image the PNG encoder always writes with an
// alpha channel, even when every pixel is opaque.
type translucentRGBA struct{ *image.RGBA }

// Opaque reports false so the encoder keeps the alpha channel.
func (translucentRGBA) Opaque() bool { return false }

// apngDelay expresses a frame delay in milliseconds as the 16-bit numerator
// and denominator (a fraction of a second) an fcTL chunk stores, using the
// finest of 1/1000, 1/100, 1/10 and 1 second that fits.
func apngDelay(ms int) (num, den uint16) {
	if ms <= 0 {
		return 0, 1000
	}
	if ms > math.MaxUint16*1000 {
		return math.MaxUint16, 1
	}
	for _, d := range []int{1000, 100, 10, 1} {
		if n := (ms*d + 500) / 1000; n <= math.MaxUint16 {
			return uint16(n), uint16(d)
		}
	}
	return math.MaxUint16, 1
}

type pngChunks struct {
	ihdr []byte
	idat []byte // concatenated IDAT payload(s)
}

// encodePNGChunks PNG-encodes an image and returns its IHDR bytes + the
// concatenated IDAT payloads.
func encodePNGChunks(img image.Image) (pngChunks, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return pngChunks{}, err
	}
	b := buf.Bytes()
	var pc pngChunks
	i := 8 // skip signature
	for i+8 <= len(b) {
		ln := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		start := i + 8
		end := start + ln
		if end > len(b) {
			break
		}
		switch typ {
		case "IHDR":
			pc.ihdr = append([]byte(nil), b[start:end]...)
		case "IDAT":
			pc.idat = append(pc.idat, b[start:end]...)
		}
		i = end + 4 // skip CRC
	}
	if pc.ihdr == nil || pc.idat == nil {
		return pngChunks{}, fmt.Errorf("png missing IHDR/IDAT")
	}
	return pc, nil
}

func writeAPNGChunk(w *bytes.Buffer, typ string, data []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	w.Write(l[:])
	w.WriteString(typ)
	w.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(typ))
	_, _ = crc.Write(data)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc.Sum32())
	w.Write(c[:])
}
