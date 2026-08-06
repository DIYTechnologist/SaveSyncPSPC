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
| PS5 → PC | ✅ (unit-tested) | ⬜ | ⬜ never confirmed in-game; writes `0` as the embedded Steam account ID, which the game may reject |

**Also pending:** `save-sync --game re2 ... --apply` has never itself been run end-to-end against a live console. The conversion *library* is proven (byte-identical to saves that loaded), but the CLI plumbing around it (`--ps5-save-name` resolution, `bridge.PCToPS5`) has only been unit-tested, not exercised for a real upload.

**Also known:** the global profile/settings slot (`data00-1.bin`) is refused outright by the engine - converting it was observed to crash the game at startup during investigation, so this isn't attempted.

## Resident Evil 3 (`re3`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ real Steam saves (all 4 slots decode, valid checksums) | ✅ real Garlic, correct `flags=0x0` PS5 container produced | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic, correct `flags=0x3` PC container produced | ⬜ never applied for real |

No known blockers - this is the most format-confirmed title without a live load test yet. Platform-identity field mapping (class `0x4a5aa7b`) was confirmed by diffing 4 real PC saves against 1 real PS5 save (multi-sample on the PC side).

## Resident Evil 4 (`re4`, engine `reengine`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ real Steam saves (Lime decrypt/encrypt round trip, checksums verify) | ✅ real Garlic, correct `flags=0x1` PS5 container produced | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic, correct `flags=0x10` Lime container produced | ⬜ never applied for real |

**Caveat carried into any live test:** the platform-identity field mapping (class `0x100e60`, enum PC=5/PS5=2) was found from a **single real sample per platform**, unlike RE3's multi-sample confirmation - and the boolean field in that class read `false` on *both* sides in the one sample checked, so it may not even be platform-discriminating for RE4. A live load test is the most direct way to find out whether the current mapping is right. See `docs/dev-res4.md`.

## Subnautica (`subnautica`, engine `unityblb`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ self-consistent round trip against real save | ✅ real Garlic | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic | ⬜ never applied for real |

No cipher, no known platform-identity field to worry about - this is the structurally simplest format in the project. No known blockers.

## Subnautica: Below Zero (`subnautica_below_zero`, engine `unityblb`)

| Direction | Format-level | Live dry-run | Live applied + in-game |
|---|---|---|---|
| PC → PS5 | ✅ | ✅ real Garlic | ⬜ never applied for real |
| PS5 → PC | ✅ | ✅ real Garlic | ⬜ never applied for real |

**Worth double-checking before a live test:** the PC↔PS5 slot pairing is hardcoded per-profile (`pc_dir: "slot0002"`) rather than matched by slot number, since the PS5 side only has one save while the PC install has three slots. Confirm `slot0002` is still the intended PC save before testing.

## Not yet implemented (no live test possible)

- **RE7, Village (RE8)** - PS5 side confirmed to share RE3's unencrypted shape, but neither is installed on this PC, so there's no `games/*.json` profile, no platform-class hash found, and nothing to test.
- **Requiem (RE9)** - genuinely encrypted with a different cipher ("Mandarin") this project hasn't implemented yet.

## Summary: what a live test session would need to cover

In rough order of how close each already is:

1. **RE3 PC→PS5, then PS5→PC** - most format-confirmed title with no live test at all.
2. **RE4 PC→PS5, then PS5→PC** - same, plus resolves the platform-field uncertainty either way.
3. **Subnautica and Subnautica: Below Zero, both directions** - simplest format, lowest risk.
4. **RE2 PC→PS5 via the actual CLI `--apply`** (not the manual recipe) - closes a real gap even though the underlying mechanism is already proven.
5. **RE2 PS5→PC, live** - first-ever live test of this direction; also tests whether the `0`-account-ID PC output is accepted.
6. **BG3 diagnostic** (duplicate-folder test, no code/no upload) - not a "live test" of a conversion, but the next real step before BG3's PS5→PC bug can even be debugged further.
7. **Clair re-confirmation** - lowest priority (presumed working, foundational), but worth doing once for a complete, fully-current record.
