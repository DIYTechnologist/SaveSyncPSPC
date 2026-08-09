# Test Cases

Status of every game/direction this tool supports, across three levels of
confidence:

- **Format-level** — real save files decoded/converted/re-parsed in Go
  (unit tests and/or manual validation against real data), no device
  involved.
- **Live dry-run** — a real `save-sync` CLI invocation against a real
  Garlic instance, pulling/producing real payloads, but not writing back
  to the PS5 or PC (no `--apply`/`--install`).
- **Live applied + in-game** — the conversion was actually written back
  (PS5 via `--apply --yes`, or PC via `--install`) *and* the game was
  launched and the save confirmed to load correctly.

Only the last level is a fully proven, ship-it-with-confidence result.
Everything below that is real progress but not sufficient to promise a
user their save will load.

## Legend

- ✅ done
- 🟡 done at a lower level only (see notes)
- ⬜ not done / not applicable
- ❌ confirmed broken

## Clair Obscur: Expedition 33 (`clair`, engine `unreal`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| Portability gate / inspect | ✅ | ✅ real Steam save + both PS5 users, all checks pass | n/a |
| PC → PS5 | ✅ | 🟡 not explicitly re-confirmed this session | 🟡 not explicitly documented in current context - this is the tool's original/foundational game, predates the session history available here |
| PS5 → PC | ✅ | 🟡 | 🟡 same caveat |

**Note:** unlike every other game below, Clair's live-conversion history predates the conversation history this document was written from. The portability *gate* was explicitly confirmed against real data; the conversion itself is presumed working (it's the tool's baseline use case) but isn't re-verified here. Worth a real re-check if you want it in this list with full confidence.

## Baldur's Gate 3 (`bg3`, engine `larian`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ | ✅ | ✅ real 39-hour Steam save loaded correctly on a real PS5 (one non-fatal "tampering" warning, plausibly a version mismatch) |
| PS5 → PC | ✅ | ✅ | ❌ **broken** - every save tested hangs at 0% on PC load, regardless of the save's origin (tested both genuinely-PS5-native content and round-tripped Steam-native content) |

**Blocking issue:** root cause unresolved. The proposed next diagnostic - copy a known-working save folder *untouched* into a new folder name under `Story/` and see if BG3 even loads that - has never been run. Until that's done, it's not known whether this is a conversion bug or BG3's own save-recognition mechanism rejecting anything not written by the game itself. See `docs/bg3.md`.

**Also pending:** the CLI-driven PC→PS5 upload (`save-sync --apply`) was run against real Garlic and produced a correct manifest, but wasn't immediately followed by its own dedicated in-game load check - the in-game confirmation above was via the manual recipe, before the CLI wiring existed. No structural reason to expect the CLI path differs, but it hasn't been separately checked.

## Resident Evil 2 (`re2`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ | ⬜ Garlic went offline before this could be run | ✅ confirmed on a real PS5 with two different saves (fresh autosave, 2.5MB manual slot) - via the underlying library/manual process, not the `save-sync --apply` CLI command itself |
| PS5 → PC | ✅ (unit-tested) | ✅ real Garlic | ✅ confirmed in-game on a real Steam Deck (2026-08-08), slot `sdimg_SAVESERVICE-LINE-0-1Slot` → `data001Slot.bin`; shares RE3's exact conversion path and inherits the account-ID fix made there (see RE3 below) |

**Also pending:** `save-sync --game re2 ... --apply` has never itself been run end-to-end against a live console. The conversion *library* is proven (byte-identical to saves that loaded), but the CLI plumbing around it (`--ps5-save-name` resolution, `bridge.PCToPS5`) has only been unit-tested, not exercised for a real upload.

**Also known:** the global profile/settings slot (`data00-1.bin`) is refused outright by the engine - converting it was observed to crash the game at startup during investigation, so this isn't attempted.

**Also known (found 2026-08-06 while picking a test slot):** RE2's "extra mode" saves (Ghost Survivors/4th Survivor, slot `21Slot`, metadata string "System saved data (extra)") use a different platform-identity class (`0x3f25bafa`) than normal story saves (`0x8b7dd7a1`, the configured `RE2.PlatformClass`), and it appears to carry only the platform enum field, not the boolean field `retargetPlatform` also expects. The existing `patched != 2` strict check in `internal/reengine/convert.go` correctly refuses to convert this save type rather than silently producing a bad one - not a bug, just an unsupported save type for now. Worth a TODO if extra-mode saves matter.

**Update (2026-08-08):** RE2 PS5→PC is now fully confirmed end to end. Live-tested against a real Steam Deck install: pulled `sdimg_SAVESERVICE-LINE-0-1Slot` from Garlic, converted with `--steam-id 11052978`, byte-verified the header/checksum, installed over `data001Slot.bin` (backing up the original first), and **confirmed loading correctly in-game**. The "writes `0` as the embedded Steam account ID" caveat that used to be here was fixed during the RE3 session (2026-08-06) - `Engine.ConvertFromPS5` now requires and forwards `--steam-id` for both RE2 and RE3, normalized to the 32-bit Steam account ID real PC saves carry; see the RE3 section below for the full root cause.

## Resident Evil 3 (`re3`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ real Steam saves (all 4 slots decode, valid checksums) | ✅ real Garlic, correct `flags=0x0` PS5 container produced | ✅ applied for real via CLI `--apply --yes` (2026-08-06), slot 0 (`sdimg_SAVESERVICE-LINE-0-0` / PC `data000.bin`); read-back confirmed byte-identical write, and the save **loaded correctly in-game** |
| PS5 → PC | ✅ | ✅ real Garlic, correct `flags=0x3` PC container produced | ✅ root cause found and fixed (2026-08-06); installed into `data001Slot.bin` and **confirmed loading correctly in-game** (verified via the "Times Saved" counter: 1 in the pre-existing PC save vs. 2 in the PS5-derived one, matching expectations) |

**Known, expected cosmetic quirk (not a bug, PC→PS5 only):** after a PC→PS5 apply, the PS5 save-select menu keeps showing the *previous* occupant's preview text (area/goal/character) for that slot until the game itself next writes to it - that text lives in the PS5's own save registration (`sce_sys/`), not in the payload this tool writes, and touching it would break the slot's registration. Confirmed harmless: the save content itself loaded correctly despite the stale menu text.

PC→PS5 is now fully confirmed end to end. Platform-identity field mapping (class `0x4a5aa7b`) was confirmed by diffing 4 real PC saves against 1 real PS5 save (multi-sample on the PC side).

### PS5→PC: the "empty slot" bug, root-caused 2026-08-06

Live-tested against a real PC install (RE3 on Steam via Proton). The converted save was installed into `data001Slot.bin` (an existing slot) and into a hand-copied `data004Slot.bin` (a slot with no prior save at all) - **neither appeared in the in-game load list**, with no error of any kind. Two bugs were found, both fixed:

**Bug 1 - the account ID was never forwarded.** `Engine.ConvertFromPS5` hardcoded the embedded Steam account ID to `0` instead of using `--steam-id`, even though the CLI flag and the underlying `ConvertPS5ToPC(data, steamID)` already supported it. Fixed; tested by `TestConvertFromPS5RequiresSteamIDForRE3` / `TestConvertFromPS5ForwardsSteamIDForRE3`.

**Bug 2 (the actual cause) - wrong *form* of Steam ID.** A PC save's ID field holds the **32-bit Steam account ID** (the number in Steam's `userdata/<id>/` path, e.g. `11052978`), but `--steam-id` was documented as, and used as, the **64-bit SteamID64** (`76561197971318706`). The game reads that field, sees a save belonging to a different account, and **silently omits it from the load list** - no error, no corruption warning, just an apparently empty slot. This is also why the empty-slot symptom was identical for a brand-new slot and an existing one, and why it looked like a missing "slot registry".

Confirmed at the byte level rather than by inference: all three real PC saves on this machine carry identical bytes at `0x18-0x20` (`24 68 18 18 f7 0a 1e 58`), our old output carried different ones, and after the fix the converted save's **entire 32-byte header is byte-identical to a real PC save's**.

The fix normalizes `--steam-id` to the 32-bit account ID at a single choke point (`bridge.steamAccountID`, applied in `resolveDynamicImages`, which both the CLI and UI server pass through), so **either form is now accepted**. RE4 is unaffected either way: its Lime cipher already masks to the low 32 bits internally (`limeExponent`), and a SteamID64's low 32 bits *are* the account ID. Tested by `TestSteamAccountIDNormalizesSteamID64` and `TestResolveDynamicImagesNormalizesSteamID`.

**Why PC→PS5 never hit this:** a PS5 container has no ID field at all (`HasID=false`) - account identity there lives in `sce_sys/param.sfo`. The wrong ID value simply never got written, so that direction worked despite the same flag being wrong.

**Confirmed in-game (2026-08-06):** loaded correctly. Both RE3 directions are now fully verified end to end.

**Separately noted (not the cause, but real):** the RSZ reader skips alignment-padding bytes without recording them and the writer re-emits zeros, so a same-offset round trip is not byte-identical (~4.4% of bytes differ, all inside alignment gaps). Real saves put non-zero bytes there. This is very likely harmless - the format's declared-size fields tell any correct parser exactly where values are, PC→PS5 works fine with zeroed padding, and gap sizes genuinely differ between the two body offsets so exact preservation isn't even well-defined across a conversion - but it is a real fidelity gap worth closing on its own merits.

## Resident Evil 4 (`re4`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ real Steam saves (Lime decrypt/encrypt round trip, checksums verify) | ✅ real Garlic, correct `flags=0x1` PS5 container produced | ✅ applied for real via CLI `--apply --yes` (2026-08-08), Steam Deck `data000.bin` → PS5 slot `sdimg_SAVESERVICE-LINE-0-0`; read-back from Garlic confirmed byte-identical write, and the save **loaded correctly in-game** |
| PS5 → PC | ✅ | ✅ real Garlic, correct `flags=0x10` Lime container produced | ✅ confirmed in-game on a real Steam Deck (2026-08-08), PS5 slot `sdimg_SAVESERVICE-LINE-0-0` → `data000.bin` |

**Update (2026-08-08):** RE4 is now fully confirmed end to end, both directions, in-game. This also resolves the platform-field mapping uncertainty below - the existing `0x100e60` mapping is correct as-is, no fix needed.

**Caveat (resolved 2026-08-08):** the platform-identity field mapping (class `0x100e60`, enum PC=5/PS5=2) was found from a **single real sample per platform**, unlike RE3's multi-sample confirmation - and the boolean field in that class read `false` on *both* sides in the one sample checked, so it wasn't certain to be platform-discriminating. Both live directions loading correctly confirms it's right as-is. See `docs/dev-res4.md`.

## Subnautica (`subnautica`, engine `unityblb`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ self-consistent round trip against real save | ✅ real Garlic | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic | 🟡 applied for real (2026-08-08) but **inconclusive** - see below |

No cipher, no known platform-identity field to worry about - this is the structurally simplest format in the project.

**2026-08-08 live test, inconclusive:** converted a real PS5 slot 0000 save and installed it into the local PC save directory (`slot0000`), backing up the original first. Verified three separate ways that the install itself is byte-perfect: the installed `global-objects.bin`/`scene-objects.bin`/`gameinfo.json` hash-match the converted output exactly, a fresh re-download of the raw PS5 `.blb` payload hashes identically to what was installed, and the files were untouched (same mtime) even after a full quit/relaunch of the game. Despite that, the user reported the game still showed the PC save's state (character in the escape pod) rather than the PS5 save's (character having swum away) - a real, distinguishing difference, not a false alarm. Left unresolved: either Garlic is serving a stale snapshot of the PS5 save (predating the "swam away" moment despite `gameTime: 95` suggesting otherwise), or the game reads save state from somewhere this investigation didn't find. Original PC save was left in place (not testing further at the time). Worth a fresh look with a PS5 save pulled immediately after a known, deliberate action in-game, to rule out staleness.

## Subnautica: Below Zero (`subnautica_below_zero`, engine `unityblb`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ | ✅ real Garlic | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic | ⬜ never applied for real |

**Worth double-checking before a live test:** the PC↔PS5 slot pairing is hardcoded per-profile (`pc_dir: "slot0002"`) rather than matched by slot number, since the PS5 side only has one save while the PC install has three slots. Confirm `slot0002` is still the intended PC save before testing.

## Resident Evil 7 (`re7`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ format-confirmed against real saves | ✅ real Garlic | ✅ confirmed loading correctly in-game (2026-08-09) - see below for the false-alarm crash first hit along the way |
| PS5 → PC | ✅ | ✅ real Garlic | ✅ confirmed in-game on a real Steam Deck (2026-08-08), PS5 slot `sdimg_SAVESERVICE-LINE-0-0` → `data000.bin` |

**New in this session (2026-08-08):** RE7 wasn't wired up at all before this - no `games/re7.json`, no `TitleConfig`. Building it surfaced a real structural difference from RE2/RE3/RE4: RE7 decrypts/parses cleanly with the predicted container shapes (PC: `KeyRE7` at offset `0x20`, `HasID=true`; PS5: unencrypted plaintext at offset `0xc`, `HasID=false`, matching RE3's shape), **but it has no platform-identity field anywhere in its object graph.** Every other title carries a settings class with a shared enum+bool pair (`0xb41fa365`/`0xe231b945`) that has to be flipped between PC/PS5; RE7's real PC save (~4300 classes) and real PS5 save (~1900 classes) were both searched exhaustively with zero matches. `TitleConfig.PlatformClass` now accepts zero to mean "skip retargeting, rely on the container-level differences alone" (`internal/reengine/convert.go`), covered by `TestConvertSkipsRetargetingWhenPlatformClassIsZero`. RE7 found on the Steam Deck via Steam app id `418370` (title ID `PPSA04400`, matching the ID list in `TODO.md`).

Live-tested PS5→PC only so far: pulled `sdimg_SAVESERVICE-LINE-0-0`, converted with `--steam-id 11052978`, byte-verified the header/checksum, installed over `data000.bin` on the Deck (backing up the original first), and **confirmed loading correctly in-game**.

### PC→PS5: one crash, then confirmed working - root cause was environmental, not the save (2026-08-08/09)

First applied for real via CLI `--apply --yes` against real Garlic, slot 0 (`data000.bin` → `sdimg_SAVESERVICE-LINE-0-0`) on 2026-08-08. Symptom on the PS5: the save-select screen showed "new content has been added"; selecting Load Game a second time showed the file, started loading, then failed with "Something went wrong with the game or application." No data was lost - a read-only pull afterward showed the slot had silently reverted to its pre-test content, presumably the PS5/game's own save-corruption recovery.

Two rounds of diagnosis followed, both using real captured files (the genuine pre-test PS5-native save, backed up at `backup/re7-20260808131657/PS5/sdimg_SAVESERVICE-LINE-0-0/data000.bin`, and the real PC source that was converted):

1. **Ruled out: our conversion corrupting the save's own content.** A field-by-field structural diff (`.scratch_verify/main.go`) between the source PC object tree and the converted PS5 object tree found zero semantic differences - every class hash, field hash, type, and value round-trips correctly. Container header/flags/offset also match a genuine PS5-native save exactly.
2. **Ruled out: our RSZ read/write path being unsound for RE7.** Built a same-platform round trip of the real native PS5 save - decode, parse, re-emit at the *same* offset (`0xc`), rebuild as an unencrypted PS5 container, with zero platform-related changes at all - and pushed that to the real PS5. It loaded correctly in-game.

**Decisive test (2026-08-09): re-pushed the exact same byte-for-byte file that crashed the first time** (`garlic_pc_to_ps5/sdimg_SAVESERVICE-LINE-0-0/data000.bin`, unmodified) to the PS5 again. **It loaded correctly.** Since the bytes were identical to the run that crashed, this rules out a save-content or conversion bug outright - the original failure was non-deterministic, consistent with jailbreak/environment instability (a flaky Garlic mount, a transient PS5 state, etc.) rather than anything wrong with the converted file. PC→PS5 is now considered confirmed working; the earlier crash is recorded here as a known false alarm rather than a live bug, in case it recurs and the pattern becomes informative.

### Full PC save inventory vs. what's actually syncable (2026-08-09)

The real PC save directory (`.../userdata/11052978/418370/remote/win64_save/` on the Steam Deck) has more files than just slot 0: `data000.bin` (slot 0), `data001Slot.bin` (slot 1), `data002Slot.bin` (slot 2), `data00-1.bin` (global profile), and `data00-3.bin` (24KB - much smaller than a real story save, an unrecognized negative-index slot, not a normal save). Ran a real `save-sync pc-to-ps5 --apply --yes` for slot 0 through the CLI (not the ad-hoc scripts used earlier in this investigation) - **synced and applied successfully**, backup taken automatically first.

**Slots 1 and 2 could not be synced**: Garlic's live save listing for `PPSA04400` only has containers for slot 0 and the two special slots (`sdimg_SAVESERVICE-LINE-0-0`, `sdimg_SAVESERVICE-LINE-0--1`, `sdimg_SAVESERVICE-LINE-0--3`) - nothing for `-1Slot`/`-2Slot`. Garlic mounts an *existing* save index; it can't create a new save container from nothing, and this project has no mechanism to either (only the game itself, saving on the PS5, allocates one). Confirmed via a dry-run CLI attempt for each: `could not find PPSA04400/sdimg_SAVESERVICE-LINE-0-1Slot in Garlic` / same for `-2Slot`. **To sync a PC slot that's never been saved to on the PS5, the PS5 has to save into that slot at least once first** (even a throwaway save), to create the container.

`data00-1.bin` (global profile) is refused by design (`profileSlotToken`, crashes the game at startup if converted - see above). `data00-3.bin` is refused too, generically, by the `n < 0` check in `fileForSlot` - it isn't a recognized slot shape at all and was never at risk of being converted.

## Resident Evil Village (`village`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ real Steam Deck save, checksum valid, 0 semantic diffs on round trip | ✅ real Garlic | ✅ applied for real via CLI `--apply --yes` (2026-08-09), slot 0 (Steam Deck `data000.bin` → PS5 `sdimg_SAVESERVICE-LINE-0-0`), backup taken automatically first, and **confirmed loading correctly in-game** on the first attempt (no false-alarm crash this time) |
| PS5 → PC | ✅ real PS5 save via Garlic, checksum valid, 0 semantic diffs on round trip | ✅ real Garlic | ✅ installed onto the real Steam Deck (2026-08-09, `--install`, backing up the live PC file first) and **confirmed loading correctly in-game** - tested twice, once against the initial round-tripped save and again after a fresh pull of a deliberately much-smaller PS5 save (417KB vs. ~1MB, an intentional early save made as a second data point) |

**New in this session (2026-08-09):** wired up from scratch - `games/village.json`, `TitleConfig` `Village` (`internal/reengine/convert.go`), `"village"` added to the `titles` map (`internal/engine/reengine/reengine.go`). Previously "not installed anywhere this project has access to"; found installed on the Steam Deck (Steam app id `1196590`, real saves at `.../userdata/11052978/1196590/remote/win64_save/`) and on the PS5 (title `PPSA01556`, slot 0 already has a container).

Confirms the docs' predictions exactly: PC side decrypts with `KeyRE8` (previously untested) at the usual offset (`0x20`, `HasID=true`), PS5 side parses as plaintext at the unencrypted offset (`0xc`), both checksums valid against real saves. **Like RE7 and unlike RE2/RE3/RE4, it has no platform-identity field** - the same exhaustive search for the shared enum+bool hash pair (`0xb41fa365`/`0xe231b945`) found zero matches in either the real PC or real PS5 save, so `TitleConfig.PlatformClass` is left at zero here too.

Both directions are now fully confirmed in-game. Real PC files live on the Steam Deck (a separate machine from where this tool runs), not locally - `--install`'s `--pc-dir` only writes to a local directory, so installing onto the Deck itself needed a manual `scp` step (backing up the Deck's live file first) after running `--install` against a local staging copy of the PC save directory. Worth automating if this remote-PC pattern comes up again.

A field-by-field structural round-trip diff (source object tree vs. converted object tree, both directions, using the real PC and real PS5 saves) found zero semantic differences. Both directions also ran cleanly as a live dry run through the actual `save-sync` CLI against real Garlic. Given RE7's PC→PS5 crash turned out to be environmental rather than a real bug, this is now at the same confidence level RE7 was at just before its own live-applied test - worth trying for real the same way.

## Not yet implemented (no live test possible)

- **Requiem (RE9)** - genuinely encrypted with a different cipher ("Mandarin") this project hasn't implemented yet.

## Summary: what a live test session would need to cover

In rough order of how close each already is:

1. ~~RE3 PS5→PC~~ - **done.** Root-caused and fixed 2026-08-06 (wrong form of Steam ID embedded), confirmed loading correctly in-game. RE3 is now fully confirmed both directions.
2. ~~RE2 PS5→PC, live~~ - **done.** Confirmed loading correctly in-game on a real Steam Deck (2026-08-08).
3. ~~RE4, both directions~~ - **done.** Both confirmed loading correctly in-game on a real Steam Deck/PS5 (2026-08-08); also resolves the platform-field mapping uncertainty (`0x100e60` mapping confirmed correct as-is).
4. ~~RE7 PS5→PC~~ - **done.** Built from scratch this session (no prior wiring) and confirmed loading correctly in-game on a real Steam Deck (2026-08-08). Discovered RE7 has no platform-identity field at all, unlike RE2/RE3/RE4.
5. **RE7 PC→PS5** - only remaining unconfirmed RE7 direction, same new code path.
6. **Subnautica PS5→PC** - applied for real (2026-08-08) but the in-game result was inconclusive (see the Subnautica section above); worth another attempt with a freshly-made PS5 save before trusting this direction. Below Zero and Subnautica PC→PS5 are both still untested live.
7. **RE2 PC→PS5 via the actual CLI `--apply`** (not the manual recipe) - closes a real gap even though the underlying mechanism is already proven.
8. **BG3 diagnostic** (duplicate-folder test, no code/no upload) - not a "live test" of a conversion, but the next real step before BG3's PS5→PC bug can even be debugged further.
9. **Clair re-confirmation** - lowest priority (presumed working, foundational), but worth doing once for a complete, fully-current record.
