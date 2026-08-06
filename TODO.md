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

Real PS5 saves captured for all five (Garlic, uid `1ea2f4da`, title IDs `PPSA03952`/`PPSA07411`/`PPSA04400`/`PPSA01556`/`PPSA31246`). All use `DSSS` magic/version 2 and all five have valid murmur3 checksums, but **which cipher a save uses varies by title *and* platform** - a title's PS5 save and its Steam save frequently don't match. Findings below were each confirmed by decrypting/parsing the real files, not inferred. See `docs/dev.md` and `docs/dev-res2.md` for the full reference.

**Key discovery method that worked** (reusable for the remaining titles): the encrypted `DSSSDSSS` check block is a known-plaintext oracle, so *any* candidate key can be tested instantly and unambiguously. Sweeping every printable-ASCII run in the whole 336MB eboot.bin against that oracle found RE4's key in ~10 seconds - far cheaper and more reliable than disassembling to find the key-schedule call site (tried first, got nowhere).

- **RE3 - wired in and live-tested.** `games/re3.json`, engine `reengine`, title `re3`. PC side: plain Blowfish+HasID (`flags=0x3`, body at `0x20`), the community-sourced `KeyRE3` (previously untested) decrypts all four real saves with valid checksums. PS5 side: unencrypted (`flags=0x0`, body at `0xc`, no key needed at all). Platform-identity fields found by diffing real PC/PS5 saves: class `0x4a5aa7b` (RE3-specific) carrying the *same* two field hashes RE2 uses (`0xb41fa365` enum, `0xe231b945` bool) - the field names are shared across the engine, only the enclosing settings class differs per title. `internal/reengine` now supports both the encrypted and unencrypted PS5 shapes generically (`TitleConfig`, `Decode`/`Build`'s `flags=0` path). **Both directions confirmed via a live CLI run against real Garlic** (dry run, `--game re3`): PC->PS5 produces a correct `flags=0x0` container, PS5->PC produces a correct `flags=0x3` container, both re-parse cleanly. Not yet confirmed: loading a converted save in-game.
- **RE4 (2023) - key found, not convertible yet.** PS5 save is plain Blowfish (`flags=0x1`, `blowfish_option=3`, body at `0x18`). Key recovered from the PS5 eboot.bin via the oracle sweep and confirmed against the real save (check block decrypts, body parses as RSZ, trailing cleartext bytes = slot 0 matching its `SAVESERVICE-LINE-0-0` container). Now `KeyRE4` in `dsss.go`, wired as `re.RE4`-shaped `TitleConfig` material but no `games/re4.json` yet (blocked below). **Blocked on the PC side**: RE4 on Steam does not use Blowfish at all - it uses "Lime" (ElGamal decrypts a per-block AES-128 key+IV, AES-OFB data, SHA3-256 per-block checksums, all keyed off the Steam account ID), so there is no fixed PC key to find and reading a PC save needs a real ElGamal implementation. Also not currently installed on this PC.
- **RE7 and Village (RE8) - same unencrypted PS5 shape as RE3, confirmed.** Both parse as plaintext RSZ at body offset `0xc`. Neither is installed on this PC, so there is no PC-side save to convert against yet; if they were, the Steam side would need checking the same way RE3's was (their `KeyRE7`/`KeyRE8` constants remain untested), and each needs its own platform-class hash found the same way RE3's was (diff real PC vs PS5 saves - the two *field* hashes are expected to match RE2/RE3's, only the class differs).
- **Requiem - genuinely encrypted, still parked.** `flags=0x10` (the `flagMandarin` bit). Does not parse as plaintext at any candidate body offset, unlike the three above. Needs the Mandarin cipher implemented in Go plus Requiem's real seed pair (`kvasszn/ree-save-editor` has a Rust `crypto/mandarin.rs` as a facts reference and a seed table, but its RE9 entry's first seed is `0` and looks like a placeholder - verify against the real save before trusting it).

**Corrections to earlier assumptions in this repo's history** (recorded so they aren't re-derived): the "Autostrong custom S-box" theory for RE3/RE7/Village was **wrong** - those PS5 saves simply aren't encrypted. `blowfish_option` is **not** a fixed-position header field; when the Blowfish flag is clear it isn't present at all, and the bytes at offset 12 are body data (which is why RE3 and Village appeared to share a "magic" value there - it was just identical RSZ header content).

## Subnautica (unityblb):

- Additional PC save slots (`slot0001`/`slot0002`) - only `slot0000` is declared for the base game today.
- Below Zero's PC-slot <-> PS5-slot pairing is hardcoded per-profile (`pc_dir`); no general mechanism exists for a user to pick which PC slot pairs with a given PS5 save at runtime.
- Loading a converted save in-game hasn't been tested for either title yet.

## Remote: 

- Support to use a NAS / Cloud Storage to auto store syncs
