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

## Usage

```go
import (
	"github.com/coreprime/kbot-io/formats/hpi"
	"github.com/coreprime/kbot-io/filesystem"
	"github.com/coreprime/kbot-io/palettes"
)
```

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
environment variable; when it is unset they fail unless `ALLOW_SKIP_ASSETS=true`
is set, in which case they skip:

```sh
ALLOW_SKIP_ASSETS=true go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
