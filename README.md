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
