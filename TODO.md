# TODO

## Unreal Engine:

- Test Games other than Clair

## Larian Engine (BG3):

- PS5 -> PC conversion is implemented but broken - every save hangs at 0% on PC load, root cause unresolved (see `docs/bg3.md`). The proposed duplicate-folder diagnostic was never run.
- Live end-to-end CLI run for PC -> PS5 was proven via the manual recipe; the CLI-driven upload itself hasn't had a separate in-game load check.

## RE Engine (RE2):

- `save-sync --game re2 ... --apply` needs one live end-to-end CLI run against a real console (the conversion library itself is already confirmed against real saves that loaded - see `docs/ressave.md`).
- PS5 -> PC is implemented and unit-tested but never confirmed in-game; it writes `0` as the embedded Steam account id, which the game may reject.
- RE3/RE4/RE5/RE6/RE7/Village/Requiem - only RE2's key/format has actually been exercised; the others share the DSSS container but are unverified.

## Subnautica (unityblb):

- Additional PC save slots (`slot0001`/`slot0002`) - only `slot0000` is declared for the base game today.
- Below Zero's PC-slot <-> PS5-slot pairing is hardcoded per-profile (`pc_dir`); no general mechanism exists for a user to pick which PC slot pairs with a given PS5 save at runtime.
- Loading a converted save in-game hasn't been tested for either title yet.

## Remote: 

- Support to use a NAS / Cloud Storage to auto store syncs
