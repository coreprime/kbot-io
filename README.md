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

### PCX and palettes

- `pcx` decodes to the PCX specification by default. `Reader.DecodeGame`
  (or `DecodeOptions{Mode: pcx.ModeGame}`) decodes a file the way TA 3.1c
  does: version 5 only, every file treated as 8-bit single-plane, rows of
  exactly `width` bytes whatever BytesPerLine says, and the palette taken from
  the last 768 bytes whether or not a 0x0C marker precedes it.
  `Reader.Compat` lists every way a file departs from those rules.
- `pal` reads the first 1,024 bytes of a `.PAL`, as the game does, and
  `pal.LoadNamed` falls back to `palettes/<name>.pcx` when the `.pal` is
  missing or empty. `pal.Table` models the lookup tables at their real sizes:
  PALETTE.ALP is 65,536 bytes (256 × 256 pair blends), PALETTE.SHD and
  PALETTE.LHT are 8,192 bytes (32 rows of 256).

## TDF text files

`formats/tdf` reads TDF, FBI, GUI and OTA text the way TA 3.1c does, and every
reader in the package (the `Document` tree, `Unmarshal`/`Decoder`,
`Canonicalize`, `SemanticEqual`) shares that one grammar:

- comments (`//` and `/* */`) are blanked byte for byte, even inside values;
- a value runs to the next `;` wherever it is, across line breaks and braces,
  so a missing `;` swallows the following text exactly as in the game;
- a `}` outside any section ends the file, a NUL byte ends the text, and only
  space, tab, CR and LF separate tokens (a UTF-8 byte order mark is text);
- a lookup by name finds the first section of that name, a key assigned
  twice keeps its last value, and keys match ignoring ASCII case only;
- numbers and flags read with the game's rules (`Atol`, `Atof`, `Fixed`,
  `Flag`), so `13O` is 13 and `canmove=true` is false.

Files the game would refuse are repaired and reported by default
(`Diagnose`, `Document.Diagnostics`); `ParseOptions{Strict: true}` refuses
them with the byte offset instead. Writers refuse keys and values the grammar
cannot carry, such as a `;` in a value or a URL's `//`.

TA: Kingdoms text is read with the same TA 3.1c grammar by default. Three
retail TA: Kingdoms files (`features/zhon/zonruin.tdf`,
`translate/messages.tdf`, `translate/customkeys.tdf`) have stray text that
now joins the following key, as it does in TA 3.1c;
`ParseOptions{SkipStrayText: true}` drops it instead, as earlier versions did.
`Strict` refuses those files and TA: Kingdoms' `.gui` files, which are not TDF
text. See the `formats/gamedata/tak` package documentation.

A struct gains a `tdf.Meta` field tagged `tdf:",meta"` to tell a missing key
from an explicit zero (the game applies non-zero defaults to many missing
keys): `Marshal` then writes every key the source had, keeps unchanged values'
text and the source order. `SemanticEqual` treats a missing key and an explicit
zero as different. `Document.Bytes` rewrites a parsed file in place, changing
only the edited values, so comments, layout and the hashes the game takes over
file text survive.

## Game data

`formats/gamedata/ta` and `formats/gamedata/tak` map FBI, weapon, feature,
moveinfo, sidedata, sound, download, GUI and OTA files onto Go structs (shared
fields live in `formats/gamedata/common`). Every struct records key presence
with `tdf.Meta` and keeps the keys and sections it does not model, so a file
decoded and marshalled again loads exactly as before in the game: explicit
zeros such as ARMSOLAR's `MaxWaterDepth=0;`, fractions such as
`metalpershot=0.5;`, the header's spelling and every section, in order. A
second section the game never reads (a second `[UNITINFO]` or `[specials]`)
is kept apart from the first, which the typed field holds.

The typed fields hold the values as written. For TA, the `Effective`
accessors return what TA 3.1c uses: its defaults for missing keys (standing
orders 2, weapon range 32767, feature `autoreclaimable` 1, OTA feature
positions -1), the widths it stores values in, and fractions an int field
cannot hold. The resolvers follow the game's lookups:

- `UnitInfo.Movement`: a unit naming a movement class takes the class's
  footprint, water depths and slopes, with the game's defaults for keys the
  class omits (10000, -10000, 255, half the maximum slope); only `[CLASS0]` to
  `[CLASS31]` exist.
- `WeaponTable`: weapons by ID from `weapons/*.tdf` only (a later file wins,
  a name resolves to the lowest slot, sections without a valid ID are skipped
  with a warning).
- `GameSides` (`SIDE0`..`SIDE4`, up to the first gap),
  `CanBuildBuilder.BuildList` (`canbuild1`.. up to the first gap, at most 30),
  `DownloadFile.Menus` (the first five sections of any name) and
  `SoundClass.Sounds` (`KEY`, `KEY1`.. up to the first gap).
- OTA: `GlobalHeader.GameSchemas` (`Schema 0`, `Schema 1`, .. up to the first
  gap), `MultiplayerSchema` (the Network 1..4 schema a game for a number of
  players uses), `Schema.StartPositions` (`StartPosN` is player slot N-1,
  `StartPos0` and unnumbered entries included), `NumPlayersText` and
  `PlayerCounts` (numplayers is display text to the game). New schemas are
  written as `[Schema N]`, with the space the game looks for and a number no
  other schema uses; `RenumberSchemas` and `RemoveSchema` name the schemas
  `Schema 0`, `Schema 1`, .. again after an edit. `SCHEMACOUNT`, which the
  game ignores, is kept as written, and a map built in code is written with
  it.

Decoded names are written back as they were. Entries built in code without
a name get one no sibling uses (`[special0]`, `[unit0]`, `[feature0]`,
`[MENUENTRY0]`, ...), never an unnamed `[]` section. Sections nested in a
weapon's `[DAMAGE]`, which the game ignores, are kept and written back.

`Check` methods and functions report what the game ignores or reads
differently: text longer than its buffers, values outside the bits it keeps,
sections it never reaches. `maplint` applies the same schema and start
position rules.

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
## PNG images and the game

The PNG and APNG files kbot-io writes (GAF frames, 3DO renders, TAF frames)
follow the PNG specification, and PNG import uses Go's `image/png`, which
applies it strictly. TA 3.1c's own PNG loader, used for images in its
Boneyards markup pages, is different: it draws each sample byte as a game
palette index (ignoring PLTE, tRNS and the colour type), skips images over
256 pixels on a side, accepts some files Go rejects and stops at some chunks
Go skips. Only 8-bit palette or greyscale PNGs holding TA palette indices,
with palette entry 0 black and no reliance on transparency, display there as
authored. The `formats/tsf` package documentation lists the differences.

## Testing

Most tests round-trip synthetic data in memory and run without any game
install. Tests that need real game assets read the `TA_UNPACKED_PATH`
environment variable (`TAK_UNPACKED_PATH` for TA: Kingdoms); when it is unset
they fail unless `ALLOW_SKIP_ASSETS=true` is set, in which case they skip:

```sh
ALLOW_SKIP_ASSETS=true go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
