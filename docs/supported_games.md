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

## Resident Evil 3 (2020)

- Game key: `re3`
- Engine: `reengine` (RE Engine DSSS container), title `re3`
- PS5 title ID: `PPSA03952`
- Region: USA
- PC target: Steam
- Save slot isn't fixed - pass `--ps5-save-name` naming the Garlic slot to target; the PC-side filename for that slot is derived automatically
- Different container shape from RE2 on both sides: PC is plain Blowfish+HasID like RE2 (confirmed against 4 real saves), but the **PS5 save is unencrypted** (`flags=0x0`, no cipher at all) rather than encrypted-no-ID
- Platform-identity fields live in a different, title-specific class (`0x4a5aa7b`) than RE2's, but the same two field hashes RE2 uses - confirmed by diffing real PC/PS5 saves
- Confirmed via a live CLI dry run (both directions) against real Garlic, output re-parses correctly on each side; not yet confirmed loading a converted save in-game

More details:

- [RE Engine family findings (RE3/RE4/RE7/Village/Requiem)](../TODO.md#re-engine-family-re3re4re7villagerequiem)
- `dev.md`'s "Resident Evil 2" section covers the shared `internal/reengine`/`TitleConfig` mechanism both RE2 and RE3 use

## Resident Evil 4 (2023)

- Game key: `re4`
- Engine: `reengine` (RE Engine DSSS container), title `re4`
- PS5 title ID: `PPSA07411`
- Region: USA
- PC target: Steam
- Save slot isn't fixed - pass `--ps5-save-name` naming the Garlic slot to target; the PC-side filename for that slot is derived automatically
- PC (Steam) side needs `--steam-id <SteamID64>` - unlike every other title here, RE4's Steam save has no fixed key at all; the account ID itself derives it (a public formula, not a secret), via a completely different cipher ("Lime") than RE2/RE3's Blowfish
- PS5 side is plain Blowfish, same shape as RE2, with its own key found via a known-plaintext oracle sweep of the real eboot.bin
- Fully validated against two real Steam saves (every block checksum verifies, full round trip byte-identical, correct slot IDs) and the complete PC→PS5→PC conversion round trip produces valid output; not yet confirmed loading a converted save in-game
- Platform-identity field mapping (class `0x100e60`) is single-sample and less certain than RE2/RE3's - see caveat in the docs below

More details:

- [RE4 deep technical reference (Lime cipher, key discovery, CLI wiring)](dev-res4.md)
- [RE Engine family findings (RE3/RE4/RE7/Village/Requiem)](../TODO.md#re-engine-family-re3re4re7villagerequiem)

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
