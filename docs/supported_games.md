# Supported Games

## Clair Obscur: Expedition 33

- Game key: `clair`
- Engine: `unreal` (GVAS)
- PS5 title ID: `PPSA17599`
- Region: EU
- PC target: Steam
- Known compatible conversion: Steam gameplay V8 <-> PS5 gameplay V7

More details:

- [Clair notes](games/clair.md)

## Baldur's Gate 3

- Game key: `bg3`
- Engine: `larian` (LSPK)
- PS5 title ID: `PPSA18463`
- Region: EU
- PC target: Steam
- Save slot and filenames aren't fixed - pass `--ps5-save-name` naming the Garlic `sdimg_SaveNNNN` slot to target
- PC->PS5 confirmed working against a real save; PS5->PC uses the same mechanism but hasn't been field-tested in-game yet

More details:

- [Dynamic image resolution / Larian engine notes](dev.md#dynamic-image-resolution-games-with-no-fixed-filenames)

## Adding Support

New games are metadata only: a `games/<key>.json` profile naming an existing engine (`unreal` or `larian`) and that engine's config - no per-game Go code needed. A genuinely new save format needs a new `internal/engine/<name>` package implementing `engine.Engine`.

See [Development Notes](dev.md) for implementation details.
