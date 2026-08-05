# Subnautica

Engine: `internal/engine/unityblb` (`games/subnautica.json`, title `PPSA02453`).

## Where the PC save lives

Unlike most Steam games, Subnautica does **not** write saves under
`%LOCALAPPDATA%`/`AppData` - it writes directly into its own install
directory: `SNAppData/SavedGames/slotNNNN` next to the game's executable
(`steamapps/common/Subnautica/SNAppData/SavedGames/slot0000` on both
native Windows and Linux/Proton, since the path is relative to the game's
own folder rather than a per-user Windows profile - the one case in this
tool where the Proton compatdata-guessing logic in `internal/pcpath`
doesn't apply and isn't needed).

A save slot directory contains:

- `gameinfo.json` - small JSON header (game time, session GUID, world
  state flags). Starts with a UTF-8 BOM on both platforms.
- `global-objects.bin`, `scene-objects.bin` - Protobuf-encoded object
  streams (confirmed by `protoBufVersion` in `gameinfo.json` and by
  `UnityEngine.Transform`-style tag strings visible in the raw bytes).
- `screenshot.jpg`
- `CellsCache/*.zip` - one zip per world-cell "batch" the player has
  actually built in or modified (e.g. `baked-batch-cells-11-grp0.zip`
  bundling several `baked-batch-cells-11-<x>-<y>.bin` cells), Stored
  (uncompressed). Real save state, not disposable cache.

## The PS5 container format

Mounting a Subnautica PS5 save shows a single file, `slotNNNN.blb`
(`sce_sys/` alongside it, untouched as always). Unlike RE2's DSSS/Blowfish
container or BG3's LSPK archive, this format has **no encryption and no
proprietary class/versioning system**:

- gzip-wrapped (confirmed via the `1f 8b 08` magic).
- Beneath the gzip, entries sit back-to-back with no directory index or
  footer: `[1-byte name length][name][4-byte little-endian size][data]`,
  repeated until EOF is the only terminator.
- Decoding a real `slot0000.blb` yields exactly the PC folder's four
  loose files plus one flattened entry per `CellsCache` cell (the zip
  wrapper doesn't exist on this side - cells sit directly in the stream
  as `CellsCache/baked-batch-cells-<batch>-<x>-<y>.bin`).
- No account ID, platform flag, or other per-platform field was found
  anywhere in the decoded data; Garlic's mount metadata (`account_id`) is
  purely Sony-side savedata bookkeeping, not embedded in the file.

This is implemented in `internal/engine/unityblb/blb.go`
(`Decode`/`Encode`) and `cellscache.go` (flattening a PC zip into
individual TLV entries, and regrouping flat PS5 entries back into
PC-shaped zips by batch id - see `groupCellsCacheIntoZips`'s doc comment
for the one open assumption: every cell for a batch id is put in a single
`grp0` zip regardless of count, since the real client's own `grp0`/`grp1`
split hasn't been observed to matter to the game's own zip reader).

## Conversion

Both directions are a straightforward pack/unpack - no alignment-sensitive
field patching like RE2, no envelope grafting like Unreal/BG3:

- **PS5→PC**: gunzip, walk the TLV entries, write the four loose files,
  group `CellsCache/*.bin` entries by batch id and zip each group back
  into `baked-batch-cells-<id>-grp0.zip`.
- **PC→PS5**: read the four loose files, unzip each `CellsCache/*.zip`
  and flatten its members into TLV entries, concatenate, gzip.

One portability check exists (`proto-version`, tier 2, overridable via
`--allow proto-version`): `gameinfo.json`'s `protoBufVersion` field must
match between the two sides, since a mismatch means a different game
build may not deserialize the transplanted save correctly.

`InstallOutputs` replaces a slot's PC directory wholesale (backing up the
original first) rather than merging files in - a stale `CellsCache` zip
left over from a previous, differently-explored save would otherwise sit
alongside the new one, mixing world state from two unrelated saves.

## Status

Confirmed byte-identical self-consistent round trip (both directions)
against a real PS5 save (`sdimg_slot0000`, title `PPSA02453`, user
"Modded") and its independent real PC save - the two are separate
playthroughs (different `gameTime`/`session`), not a transferred pair, so
their *content* isn't expected to match; what was verified is that
PC→PS5→PC and PS5→PC→PS5 reproduce each side's own bytes exactly. Also
confirmed via a live end-to-end `save-sync` CLI run (both directions)
against a real Garlic instance, `--ps5-uid` `1ea2f4da`, gunzip/re-gzip and
CellsCache regrouping/flattening working correctly on real data. Not yet
tested: actually loading a converted save in-game.

Only `slot0000` is declared in `games/subnautica.json` today, since that's
the only save slot exercised so far; a second/third slot just needs
another `images` entry (`sdimg_slot0001`/`slot0001.blb`/`slot0001`, etc. -
Subnautica supports up to 3 slots per the same UI as Subnautica: Below
Zero, which shares this save layout but isn't wired up as its own profile
yet).
