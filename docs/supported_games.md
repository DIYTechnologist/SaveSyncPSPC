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

## Resident Evil 2 (2019)

- Game key: `re2`
- Engine: `reengine` (RE Engine DSSS container)
- PS5 title ID: `PPSA04288`
- Region: USA
- PC target: Steam
- Save slot isn't fixed - pass `--ps5-save-name` naming the Garlic slot to target; the PC-side filename for that slot is derived automatically
- PC->PS5 confirmed working against a real save (two different saves loaded correctly); PS5->PC is implemented and unit-tested but not yet confirmed in-game
- The global profile/settings slot (`data00-1.bin`) is refused outright - converting it crashed the game at startup

More details:

- [RE2 investigation log](ressave.md)
- [RE2 deep technical reference (container format, RSZ, and the full eboot.bin disassembly)](dev-res2.md)

## Subnautica

- Game key: `subnautica`
- Engine: `unityblb` (gzip + flat length-prefixed entries)
- PS5 title ID: `PPSA02453`
- Region: USA
- PC target: Steam
- Save slot is fixed (`slot0000`) - only the one slot a real save has been confirmed against is declared today; a second/third slot needs another `images` entry in `games/subnautica.json`
- No encryption, no proprietary class/versioning system, no platform field in the save data itself - by far the simplest format this tool handles
- Confirmed byte-identical round trip (both directions) against a real PS5 save and its PC equivalent, and a live end-to-end CLI run (both directions) against a real Garlic/PS5

More details:

- [Subnautica notes](subnautica.md)

## Subnautica: Below Zero

- Game key: `subnautica_below_zero`
- Engine: `unityblb` (identical format to Subnautica)
- PS5 title ID: `PPSA02457`
- Region: USA
- PC target: Steam
- PC has 3 save slots but PS5 has only 1; the profile's `pc_dir` is hardcoded to whichever PC slot pairs with the existing PS5 save (`slot0002` today) - see "Below Zero" in `subnautica.md` for why this can't be auto-detected
- Confirmed byte-identical round trip (both directions) against a real PS5 save and its paired PC save, and a live end-to-end CLI run (both directions) against a real Garlic/PS5

More details:

- [Subnautica notes](subnautica.md#below-zero)

## Adding Support

New games are metadata only: a `games/<key>.json` profile naming an existing engine (`unreal`, `larian`, `reengine`, or `unityblb`) and that engine's config - no per-game Go code needed. A genuinely new save format needs a new `internal/engine/<name>` package implementing `engine.Engine`.

See [Development Notes](dev.md) for implementation details.
