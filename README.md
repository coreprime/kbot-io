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

A struct gains a `tdf.Meta` field tagged `tdf:",meta"` to tell a missing key
from an explicit zero (the game applies non-zero defaults to many missing
keys): `Marshal` then writes every key the source had, keeps unchanged values'
text and the source order. `SemanticEqual` treats a missing key and an explicit
zero as different. `Document.Bytes` rewrites a parsed file in place, changing
only the edited values, so comments, layout and the hashes the game takes over
file text survive.

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
environment variable; when it is unset they fail unless `ALLOW_SKIP_ASSETS=true`
is set, in which case they skip:

```sh
ALLOW_SKIP_ASSETS=true go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
