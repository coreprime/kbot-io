# kbot-io

`kbot-io` is a self-contained Go module providing reusable parsers, encoders,
and a virtual filesystem for the classic *Total Annihilation* and
*TA: Kingdoms* game data formats. It is the shared I/O layer extracted from the
[`kbot`](https://github.com/coreprime/kbot) toolchain so that other projects can
depend on the format code without pulling in the full CLI.

## Packages

- **`formats/`** — parsers and writers for the game data formats, including:
  - `hpi` (HPI/GP3 archives, v1 and v2), `gaf`/`tsf` (sprite banks), `pcx`,
    `pal` (palettes), `tnt`/`sct` (maps and terrain, incl. TA:K), `fnt` (fonts),
    `crt`, `bik`/`smacker` (video headers, plus decode-only MP4 conversion
    through FFmpeg), `objects3d` (3DO/TDO models),
    `tdf` (config files), `gamedata` (unit/weapon definitions for TA and TA:K),
    `scripting` (COB scripting: parser, compiler, decompiler, assembly, linter),
    and `ai`. The COB tools follow the game's rules: opcodes are decoded the
    way TA 3.1c dispatches them, the compiler refuses code the game would
    fault on or mis-run, the decompiler reports scripts it cannot write as
    equivalent BOS, and the linter flags TA: Kingdoms-only instructions in TA
    scripts (see the `formats/scripting` package documentation).
- **`filesystem/`** — a layered virtual filesystem (`vfs`) that transparently
  reads files from packed HPI archives and loose directories.
- **`palettes/`** — the embedded default TA color palette plus the per-kingdom
  TA:K texture palettes.
- **`testutil/`** — test helpers for locating optional unpacked game assets.

### GAF

`formats/gaf` reads GAF files the way TA 3.1c does: the sequence count is the
signed low 16 bits of its word, durations are 16-bit ticks at 30 per second,
frame bytes +10 and +11 are a layer count and a separate blend flag, and any
version word is accepted. Structures the game reads differently or cannot
load (nested or shared frame headers, short compressed rows, odd header
words) are reported by `Reader.Warnings`. The writer keeps each frame's raw
or compressed storage, the sequence loop word and composite layers;
`gaf.StorageForPath` names the archives the game needs raw (unit textures and
the sight masks in `anims/vismasks.gaf`). Reading is bounded: the pixel data
produced plus the compressed row bytes read may not exceed 512 MiB per file.
Palettes are fully opaque (index 0 is black); by default exports make only a
raw frame's stored key, or a compressed frame's skipped pixels, transparent,
and `TransparencyModeHeuristic` is available for TA: Kingdoms atlases. See
the package documentation for the full rules.

## Usage

```go
import (
	"github.com/coreprime/kbot-io/formats/hpi"
	"github.com/coreprime/kbot-io/filesystem"
	"github.com/coreprime/kbot-io/palettes"
)
```

## HPI archives and the virtual filesystem

The HPI readers and the VFS resolve files the way Total Annihilation 3.1c does:

- **Which archives mount.** By default a TA directory is scanned the way the
  game scans it: top level only, `rev31.gp3`, then `*.ccx`, `*.ufo` and the
  first ten `*.hpi` that open, each group in name order with ASCII letters
  upper-cased. The first archive holding a path wins and loose files beat every
  archive. An archive mounts only if it is a version 1 archive ending with the
  36-byte `Copyright ____ Cavedog Entertainment` trailer; others are skipped
  and listed by `SkippedArchives`, and do not count toward the ten. A
  directory whose archives are all TA: Kingdoms (version 2) archives keeps the
  all-archives order TA: Kingdoms needs (`data.hpi` overlaid by `IPData.hpi`);
  `Config.Discovery` selects either mode explicitly. `MountOrder` reports the
  resulting precedence and `ListGameOrder` enumerates a directory in the
  game's order, duplicates included.
- **Lookups.** `\` and `/` both separate paths on every host, only ASCII
  letters fold case, the last of several same-named entries wins, and a path
  that is unreachable in one archive falls through to the next.
- **Reading.** Header key bytes 0 and 0xFF mean "not encrypted"; chunks are
  located through the chunk-size table and placed in fixed 64 KiB blocks; SQSH
  type 0 is refused inside chunked entries; LZ77 chunks must terminate and
  match their size; zlib chunks follow the game's lenient length rule
  (`v1.ReadOptions.Strict` makes them strict).
- **Writing.** The v1 writer requires the Cavedog trailer, writes compression
  method 0 as stored entries, merges directories ignoring case and refuses
  empty, `.` and `..` path segments and names over 255 bytes.
- **Checking an archive.** `hpi.Validate` reports the version, header and
  effective key, trailer, and whether TA 3.1c would mount the file.

## Testing

Most tests round-trip synthetic data in memory and run without any game
install. Tests that need real game assets read the `TA_UNPACKED_PATH`
environment variable; when it is unset they fail unless `ALLOW_SKIP_ASSETS=true`
is set, in which case they skip:

```sh
ALLOW_SKIP_ASSETS=true go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
