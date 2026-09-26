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
    `crt`, `bik`/`smacker` (video), `objects3d` (3DO/TDO models),
    `tdf` (config files), `gamedata` (unit/weapon definitions for TA and TA:K),
    `scripting` (COB scripting: parser, compiler, decompiler, assembly, linter),
    and `ai`.
- **`filesystem/`** — a layered virtual filesystem (`vfs`) that transparently
  reads files from packed HPI archives and loose directories.
- **`palettes/`** — the embedded default TA color palette plus the per-kingdom
  TA:K texture palettes.
- **`testutil/`** — test helpers for locating optional unpacked game assets.

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
