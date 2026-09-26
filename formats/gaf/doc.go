// Package gaf implements reading and writing of Total Annihilation and TA:
// Kingdoms GAF (Graphics Animation Format) files.
//
// The on-disk binary layout is identical between both games; only palette
// resolution differs. See Variant for how callers signal which game an
// asset belongs to.
//
// # Layout
//
// All values are little-endian.
//
//	header (12 bytes)        version, sequence count, unused word
//	sequence pointers        one uint32 offset per sequence
//	sequence header (40)     frame count (uint16), loop word (uint16),
//	                         unused word (uint32), name (32 bytes, NUL-padded)
//	frame list               per frame: frame header offset, duration word
//	frame header (24)        width, height, origin x/y (int16), key byte,
//	                         storage byte, layer count byte (+10), blend
//	                         byte (+11), word +12, data offset, word +20
//
// # Reading rules
//
// The reader follows the way TA 3.1c reads these files:
//
//   - The version word is not checked. Stock files use VersionTA or 0; any
//     other value is accepted and reported by Reader.Warnings.
//   - The sequence count is the low 16 bits of its word, read as a signed
//     value: 1 to 32767 sequences, and none for zero or a negative value.
//   - The low byte of the sequence loop word (Sequence.LoopFlags) says
//     whether the sequence loops (every stock sequence stores 1) or plays
//     once.
//   - A frame's duration is the low 16 bits of its word, in ticks of the
//     30 Hz animation clock. A frame shows for max(duration, 1) ticks.
//   - Byte +10 of a frame header is its layer count and byte +11 a separate
//     flag (Frame.Blend); they are not one 16-bit count. A composite frame
//     (layer count above zero) points at a table of layer header offsets.
//     The game draws each layer so that its hotspot lands on the frame's
//     hotspot, and draws a layer whose +11 byte is set translucently.
//   - A storage byte of 0 means raw pixels (Width*Height palette indices),
//     any other value row compression. Raw frames are drawn with every
//     pixel except those equal to the frame's transparency index (key);
//     compressed frames draw every repeated or copied pixel, even one equal
//     to the key, and only skip commands are transparent (Frame.Opaque
//     records the difference when it matters). Palette index 0 is ordinary
//     opaque black.
//   - A frame with no pixels reads no data, whatever its data offset.
//
// The reader keeps every sequence and frame field it reads, including the
// words the game does not interpret, so that a rewritten file keeps them (the
// writer lays the file out afresh and always writes the header version
// VersionTA). It flattens each composite frame into Pixels (clipped to the
// frame's own rectangle) and also keeps the layers in Frame.Layers.
//
// Some structures the game cannot load are read anyway and reported by
// Reader.Warnings: a layer that is itself a composite or refers back to its
// own frame (skipped), and a frame header referenced from more than one
// place (decoded once per reference). A compressed row whose commands end
// before the frame width is padded with transparent pixels and reported; the
// game would read on into the following bytes. No stock file has any of
// these.
//
// Reading is bounded: a frame may hold at most 64 Mi pixels and one file at
// most 2^20 frame headers. One ReadSequences call may use at most 512 MiB of
// frame data in total, counting both the pixel, coverage and canvas bytes it
// produces and the compressed row bytes (size words included) it reads, once
// per frame header that points at them. A small crafted file therefore cannot
// exhaust memory or keep the reader busy for long; the largest stock file
// needs about 123 MiB.
//
// # Writing
//
// WriteGAF and WriteGAFWith write back everything the reader keeps: the
// sequence loop and +4 words, each frame's storage, +11 byte and unknown
// words, and the layers of composite frames. Frames built in memory have
// StorageDefault and are compressed unless WriteOptions.DefaultStorage says
// otherwise. The game reads unit textures (textures/*.gaf) and the sight
// masks (anims/vismasks.gaf) as plain pixel arrays, so frames written there
// must be raw; StorageForPath returns the storage a path needs. A sequence
// built in memory has LoopFlags 0 and plays once in the game; use SetLoops to
// make it loop, as every stock sequence does.
//
// Each frame gets its own header, since the game cannot load a file whose
// frame headers are shared. The writer refuses, without writing anything,
// input the format cannot hold: more than 32767 sequences, 65535 frames in a
// sequence or 255 layers in a frame, layers with layers of their own, names
// over 32 bytes, durations over 65535 ticks, pixel buffers of the wrong size
// and compressed rows over 65535 bytes.
//
// # Rendering and transparency
//
// The exporters (ToImage, ToPNG, ToGIF, ToAPNG and their ...With forms)
// produce indexed images whose pixels keep their palette index. Every
// palette entry is opaque, palette index 0 (black) included, except one
// transparent slot chosen per export. The zero RenderOptions applies the
// game's rule (the stored key for raw frames, the skipped pixels for
// compressed and composite frames); TransparencyModeNone makes every pixel
// opaque, TransparencyModeIndex makes one chosen index transparent, and
// TransparencyModeHeuristic guesses a key from the corner pixels of raw
// frames, which suits TA: Kingdoms atlases and is what
// VariantTAK.DefaultRenderOptions returns. Normally the slot is the frame's
// key; when a frame draws key-valued pixels, or frames of one animation use
// each other's keys as colours, an index no drawn pixel uses becomes the
// slot instead. Layers with the +11 flag are exported as ordinary pixels.
//
// Animated exports follow the game's timing: a frame shows for
// Frame.DisplayTicks ticks of 1/30 s (APNG delays are exactly ticks/30; GIF
// delays are rounded to hundredths on the running total), and the animation
// loops forever only when the sequence loops, otherwise it plays once.
package gaf
