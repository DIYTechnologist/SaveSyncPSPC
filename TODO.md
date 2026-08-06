# TODO

## Unreal Engine:

- Test Games other than Clair

## Larian Engine (BG3):

- PS5 -> PC conversion is implemented but broken - every save hangs at 0% on PC load, root cause unresolved (see `docs/bg3.md`). The proposed duplicate-folder diagnostic was never run.
- Live end-to-end CLI run for PC -> PS5 was proven via the manual recipe; the CLI-driven upload itself hasn't had a separate in-game load check.

## RE Engine (RE2):

- `save-sync --game re2 ... --apply` needs one live end-to-end CLI run against a real console (the conversion library itself is already confirmed against real saves that loaded - see `docs/ressave.md`).
- PS5 -> PC is implemented and unit-tested but never confirmed in-game; it writes `0` as the embedded Steam account id, which the game may reject.

## RE Engine family (RE3/RE4/RE7/Village/Requiem):

Real PS5 saves captured for all five (Garlic, uid `1ea2f4da`, title IDs `PPSA03952`/`PPSA07411`/`PPSA04400`/`PPSA01556`/`PPSA31246`). All use `DSSS` magic/version 2 and all five have valid murmur3 checksums, but **which cipher a save uses varies by title *and* platform** - a title's PS5 save and its Steam save frequently don't match. Findings below were each confirmed by decrypting/parsing the real files, not inferred. See `docs/dev-res2.md` for the RE2 reference this extends.

**Key discovery method that worked** (reusable for the remaining titles): the encrypted `DSSSDSSS` check block is a known-plaintext oracle, so *any* candidate key can be tested instantly and unambiguously. Sweeping every printable-ASCII run in the whole 336MB eboot.bin against that oracle found RE4's key in ~10 seconds. This is far cheaper and more reliable than disassembling to find the key-schedule call site (which was tried first and got nowhere - the P-array constants are present in rodata but nothing referenced them in a way a static scan could follow).

- **RE4 (2023) - key found, not convertible yet.** PS5 save is plain Blowfish (`flags=0x1`, `blowfish_option=3`, body at `0x18`). Key recovered from the PS5 eboot.bin via the oracle sweep above and confirmed against the real save (check block decrypts, body parses as RSZ, trailing cleartext bytes = slot 0 matching its `SAVESERVICE-LINE-0-0` container). Now `KeyRE4` in `dsss.go`. **Blocked on the PC side**: RE4 on Steam does not use Blowfish at all - it uses "Lime" (ElGamal decrypts a per-block AES-128 key+IV, AES-OFB data, SHA3-256 per-block checksums, all keyed off the Steam account ID), so there is no fixed PC key to find and reading a PC save needs a real ElGamal implementation. Also not currently installed on this PC, so there is nothing to convert against even if implemented.
- **RE3 - the best next target.** Owned on *both* platforms and both sides now decode:
  - PC (Steam, appid 952060, `~/.local/share/Steam/userdata/<id>/952060/remote/win64_save`): plain Blowfish + HasID, exactly RE2's PC shape (`flags=0x3`, body at `0x20`). The community-sourced `KeyRE3` (previously carried but never tested) **works** - all four real saves decode with valid checksums, correct embedded slot numbers (`-1`/`0`/`1`/`2`), and steamID `11052978`.
  - PS5: **completely unencrypted** - `flags=0x0`, no `blowfish_option` field at all, body starts at `0xc` and parses as valid RSZ directly.
  - Work needed: teach `Decode`/`Build` the unencrypted `flags=0x0` layout (body at `0xc`, no option field), then conversion is the same PC↔PS5 realignment problem RE2 already solved (`0x20` vs `0xc` here instead of `0x20` vs `0x18`), plus whatever platform-identity fields RE3 uses. No new cipher required.
- **RE7 and Village (RE8) - same unencrypted PS5 shape as RE3.** Both parse as plaintext RSZ at body offset `0xc`. Neither is installed on this PC, so there is no PC-side save to convert against yet; if they were, the Steam side would need checking the same way RE3's was (their `KeyRE7`/`KeyRE8` constants remain untested).
- **Requiem - genuinely encrypted, still parked.** `flags=0x10` (the `flagMandarin` bit). Does not parse as plaintext at any candidate body offset, unlike the three above. Needs the Mandarin cipher implemented in Go plus Requiem's real seed pair (`kvasszn/ree-save-editor` has a Rust `crypto/mandarin.rs` as a facts reference and a seed table, but its RE9 entry's first seed is `0` and looks like a placeholder - verify against the real save before trusting it).

**Corrections to earlier assumptions in this repo's history** (recorded so they aren't re-derived): the "Autostrong custom S-box" theory for RE3/RE7/Village was **wrong** - those PS5 saves simply aren't encrypted. `blowfish_option` is **not** a fixed-position header field; when the Blowfish flag is clear it isn't present at all, and the bytes at offset 12 are body data (which is why RE3 and Village appeared to share a "magic" value there - it was just identical RSZ header content).

## Subnautica (unityblb):

- Additional PC save slots (`slot0001`/`slot0002`) - only `slot0000` is declared for the base game today.
- Below Zero's PC-slot <-> PS5-slot pairing is hardcoded per-profile (`pc_dir`); no general mechanism exists for a user to pick which PC slot pairs with a given PS5 save at runtime.
- Loading a converted save in-game hasn't been tested for either title yet.

## Remote: 

- Support to use a NAS / Cloud Storage to auto store syncs
