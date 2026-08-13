# TODO

## Unreal Engine:

- Test Games other than Clair

## Larian Engine (BG3):

- PS5 -> PC conversion is implemented but broken - every save hangs at 0% on PC load, root cause unresolved (see [savesync-engine's `docs/bg3.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/bg3.md)). The proposed duplicate-folder diagnostic was never run.
- Live end-to-end CLI run for PC -> PS5 was proven via the manual recipe; the CLI-driven upload itself hasn't had a separate in-game load check.

## RE Engine (RE2):

- `save-sync --game re2 ... --apply` needs one live end-to-end CLI run against a real console (the conversion library itself is already confirmed against real saves that loaded - see [savesync-engine's `docs/ressave.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/ressave.md)). PS5 -> PC is now fully confirmed in-game (2026-08-08, see [savesync-engine's `TestCases.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/TestCases.md)).

**RE Engine family findings (cipher/key discoveries, RSZ alignment bug, per-title RE3/RE4/RE7/Village/Requiem confirmation) and The Alters (`elbsave`) format investigation both now live in [savesync-engine's `TODO.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/TODO.md)** - they're engine/format-level facts, not CLI tasks, and moved there along with the rest of the format library. Remaining CLI-level task for RE2: the extra-mode-saves gap is tracked there too.

## Subnautica (unityblb):

- Additional PC save slots (`slot0001`/`slot0002`) - only `slot0000` is declared for the base game today.
- Below Zero's PC-slot <-> PS5-slot pairing is hardcoded per-profile (`pc_dir`); no general mechanism exists for a user to pick which PC slot pairs with a given PS5 save at runtime.
- Loading a converted save in-game hasn't been tested for either title yet.

## Remote: 

- Support to use a NAS / Cloud Storage to auto store syncs
