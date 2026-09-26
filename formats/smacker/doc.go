// Package smacker reads RAD Game Tools Smacker video headers, the format of
// Total Annihilation's cinematics (shipped as data/*.zrb), and converts
// Smacker movies to MP4 through FFmpeg.
//
// # File layout
//
// A Smacker file starts with a 104-byte little-endian header (Header):
// signature, width, height, frame count, frame rate, flags, the largest
// audio chunk of each track, the Huffman tree sizes, one packed audio word
// per track and an unused dword. The frame-size table (one dword per entry)
// starts at byte 104 (HeaderSize), followed by the frame-type table (one
// byte per entry), the Huffman trees and the frame payloads. The tables hold
// one entry per frame, plus one for the ring frame when FlagRingFrame is
// set. The game uses each frame-size entry whole as the payload size.
//
// # Versions
//
// TA 3.1c plays SMK2 files only. SMK4 files from newer tools still open, so
// they can be inspected; Version reports the version, Info notes it, and
// Validate reports SMK4 as ErrUnsupportedVersion.
//
// # Frame rate
//
// A positive frame-rate field is milliseconds per frame and a negative one
// is hundred-thousandths of a second per frame; the shipped movies store
// -3333, about 30 fps. A zero field has no defined timing: FrameRate reports
// DefaultFrameRate (15 fps) for it and ConvertToMP4 times the MP4 at that
// rate. The rate TA 3.1c plays such a file at has not been established, and
// other Smacker decoders use 10 fps.
//
// # Audio
//
// Each of the seven tracks has a packed audio word: the sample rate in the
// low 24 bits and flags in the high byte (AudioCompressed, AudioPresent,
// Audio16Bit, AudioStereo). The game uses a track only when its present bit
// is set, whatever its rate. AudioTrack and AudioTracks decode the words;
// the shipped movies have one present track, 22050 Hz, stereo, 8-bit and
// compressed.
//
// # Display height
//
// FlagInterlaced makes the game show a movie at twice its stored height with
// every second line black, and FlagDoubled shows every stored line twice; a
// header with both bits set is shown at its stored height. The shipped
// 640x240 movies are interlaced and appear as 640x480. HeightMode and
// DisplayHeight report this, and ConvertToMP4 produces it.
//
// # Limits and validation
//
// Opening a file checks that the tables and trees its header declares fit in
// the file before allocating them, so a corrupt count cannot cause a large
// allocation. Frame payloads are not read. Validate applies further
// structural checks against Limits, such as whether the payloads end within
// the file.
//
// # Conversion
//
// Pixels and audio are not decoded here. ConvertToMP4 hands the file to
// FFmpeg, which decodes Smacker natively, and writes H.264 and AAC at the
// display height with square pixels, stopping at the header's frame count
// (a ring frame is not shown). There is no Smacker writer: FFmpeg has no
// Smacker encoder or muxer and this package does not encode Smacker, so
// ConvertFromMP4 returns ErrNoSmackerWriter unless the installed FFmpeg
// lists both. SMK2 movies for TA are made with RAD's Smacker tools.
package smacker
