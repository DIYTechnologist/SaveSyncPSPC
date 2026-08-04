# Resident Evil 2 (2019) Save Format — Investigation Notes

Research spike into whether `save-sync`'s PC↔PS5 bridge could be extended to Resident Evil 2 (Capcom's RE Engine), prompted by the user having both a PC (Steam) and PS5 copy with real saves on each. The container format, cipher, and checksum are now fully understood and implemented in `internal/reengine`, validated byte-for-byte against real game-written files. **PC → PS5 conversion does not work in-game** despite that. A field-level parser was subsequently built and now reads both platforms' saves completely, which established that the field schema is *identical* across platforms — so the cause lies outside both the container and the save data itself. This document is a research writeup, not a supported-game guide — there is no `games/re2.json` and no `engine.Engine` implementation. `internal/reengine` is not wired into the rest of the tool.

## Status

| Direction | Status |
|---|---|
| Decode/re-encode a save (either platform) | Works, byte-exact |
| PC → PS5 conversion | **Fails in-game** at three different points depending on which files are touched (see "Live-device findings") |
| PS5 → PC conversion | Not attempted |
| RSZ field parsing (both platforms) | Works — all real saves parse fully |

## The container format ("DSSS")

RE Engine's save files (RE2/RE3/RE7/RE8 share this shape; RE4 and some others use a different cipher) start with a 4-byte magic and a small fixed header:

```
offset 0x00  "DSSS"            magic
offset 0x04  u32 = 2           version (only 2 observed/handled)
offset 0x08  u32               flags (bitfield: 0x1 Blowfish, 0x2 HasID, 0x4 Citrus, 0x8 Deflate, 0x10 Mandarin/Lime)
offset 0x0C  u32               blowfish_option (3 on every real save observed; 0 means "not encrypted", other values unhandled)
offset 0x10  [8 bytes]         encrypted "DSSSDSSS" - a self-check block, decrypts to that literal ASCII string
offset 0x18  [8 bytes, only if HasID]  encrypted account/Steam ID, aligned up to the next 8-byte boundary first
             [N bytes]         the encrypted body (see below)
             [4 bytes]         murmur3_32(everything before this, seed=0xffffffff), little-endian
```

**PC saves set both Blowfish and HasID.** The HasID field holds the Steam account ID.
**PS5 saves set only Blowfish** — no ID field. Account identity there comes from the PS5 container itself (`sce_sys/param.sfo`'s `ACCOUNT_ID`), not from inside the `.bin` payload. This is the one structural difference between the two platforms' containers; everything else about the container shape is identical.

### The body's length is not required to be a multiple of 8

This was a real bug, found by round-tripping real files and catching a byte count mismatch. Blowfish only covers the 8-byte-aligned *prefix* of the body; RE Engine stores any trailing 1–7 bytes in the clear. On every real save observed, those trailing bytes are the save's own slot number as a little-endian `u32`:

| File | Trailing bytes (decoded) | Meaning |
|---|---|---|
| `data000.bin` | `00 00 00 00` | slot 0 (autosave) |
| `data00-1.bin` | `ff ff ff ff` | slot −1 (global profile) |
| `data001Slot.bin` / `data002Slot.bin` | `01 00 00 00` / `02 00 00 00` | manual slots 1, 2 |
| `data021Slot.bin` | `15 00 00 00` | slot 21 |

An earlier version of `Decode` silently truncated these bytes (Blowfish-CBC decrypt only processes whole blocks). Every early PC→PS5 upload was therefore missing its slot ID. Fixed by carrying the cleartext remainder through on both decode and encode.

## The cipher: Blowfish, little-endian variant

Standard Blowfish (Schneier's original 1993 cipher — public domain, not Capcom-proprietary), CBC mode, an all-zero IV, no padding. The only non-standard part is that RE Engine reads/writes each 8-byte block's two 32-bit halves as **little-endian** integers, where textbook Blowfish (and Go's `golang.org/x/crypto/blowfish`) uses big-endian.

`internal/reengine/blowfish.go` implements this by wrapping the standard big-endian cipher: reverse each 4-byte half's byte order before encrypting/decrypting a block, then reverse back. This reproduces a native little-endian implementation exactly, since byte order is purely a serialization convention around the same underlying arithmetic — verified against Bruce Schneier's own published zero-key/zero-plaintext test vector for the *underlying* big-endian primitive (`4ef997456198dd78`), and then against real save files for the LE wrapping itself.

**Per-title keys** (community-documented, fixed constants baked into each game's binary — not derived from account/save data):

```go
KeyRE2 = "K<>$cl%isqA|~nV4W5~3z_Q)j]5DHdB9sb{cI9Hn&Gqc-zO8O6zf"
KeyRE3 = "mAz{]jeQ+uxyNH*d<Dt2kC5r=3M9RV6c$TaG[b|}^%&)En4F(Wvp"
KeyRE7 = "hHGb4nS653aRT29jy"
KeyRE8 = "j1lL1AOR31sd4HKJS90fs"
```

Only `KeyRE2` has actually been exercised against real saves (both platforms, confirmed via the `DSSSDSSS` self-check and by successfully decrypting real content). RE3/RE7/RE8 are recorded because they cost nothing to carry, but are unverified.

## The checksum

Standard MurmurHash3 (x86, 32-bit) — Austin Appleby's public-domain hash, not RE-Engine-specific — over the whole file except the trailing 4 hash bytes, seed `0xffffffff`. Implemented from the public algorithm spec and cross-checked against 5 independent vectors from Python's `mmh3` library.

## What's been formally reused vs. reimplemented

Per explicit user decision mid-investigation: the container format, cipher choice/mode, and per-title keys were reused from community reverse-engineering (primarily [kvasszn/ree-save-editor](https://github.com/kvasszn/ree-save-editor)'s Rust source, read for facts, not ported) rather than independently discovered from scratch — unlike this project's other format work (BG3's LSPK, Unreal's GVAS), which were built purely from empirical byte analysis. The actual Go implementation here (Blowfish-LE wrapper, murmur3, header parser/writer) is original, not copied, and every fact taken from the reference was independently verified against real save files before being trusted.

## `internal/reengine/rsz.go` — the RSZ field parser (diagnostic, build-tagged)

RE Engine's field data (inventory, room-interaction flags, entity transforms — everything past the container header) is a tagged-object format nicknamed "RSZ" by the modding community: each field is a `(hash, type-tag, value)` tuple, self-describing enough to walk without any external schema (the type and, for variable-length values, the size are read straight from the binary).

**It now parses every real save on both platforms** — all ten files available during development (five PC, five PS5; autosaves, manual slots, and global profiles) parse to completion with zero unknown-type fields. Getting there required one non-obvious fix.

### The alignment-base bug

Field alignment is computed in **file coordinates — including the container header — not relative to the body.** This matters because the two platforms' headers are different sizes:

- A **PC** body starts at `0x20` (32 bytes). 32 is 16-aligned, so body-relative and file-relative alignment agree.
- A **PS5** body starts at `0x18` (24 bytes). 24 is 8-aligned but *not* 16-aligned, so every 16-byte alignment inside the body lands 8 bytes away from where a body-relative calculation puts it.

The consequence was pathological: PC saves parsed perfectly (135,748 fields on a 2.5MB save) while PS5 saves desynchronised almost immediately, and the desync *looked* like a format mystery rather than an arithmetic bug. It surfaced as fields with a zero hash and zero type tag, which read like legitimate "empty" entries — and even the reference Rust implementation carries a `// TODO: Add Struct weird shit handling` comment at the corresponding spot, which made "the Struct encoding is unmapped" a very plausible-looking wrong answer.

What actually settled it was a differential test rather than more hex archaeology: dumping class `0x3b9a2a09` from the PC save (which parsed cleanly) gave a known-good 9-field layout, and comparing the PS5 offsets against it showed the struct payloads sitting at body offsets ≡ 8 (mod 16) — exactly one header's worth of skew. `ReadRSZObjects` now takes the body's `DataOffset` and aligns against it; `rsz_test.go` pins this with a synthetic body parsed at both bases.

The reader also bounds declared field counts and array lengths against the bytes actually remaining, so a desync produces a diagnosable error instead of a multi-gigabyte allocation.

### Schema comparison: PC vs PS5

With both platforms parsing, the payoff was a structural diff — comparing every class's field signature (hashes and type tags) across platforms, which needs none of Capcom's naming schema:

- **124 distinct classes** in the PC autosave, **84** in the PS5 autosave.
- **Zero classes share a hash but differ in field layout.** The RSZ schema is *identical* across platforms.
- 41 classes appear only in PC and 1 only in PS5 — but this is a content difference, not a structural one: the PC save is hours into the campaign while the PS5 save is a fresh start, so it simply contains fewer object types. The single PS5-only class (`0xbac8010a`) was inspected directly and holds a position/rotation transform plus flags — a world entity present early in the game, not a platform marker.

**This is a significant negative result:** there is no schema-level reason a PC save body should be unloadable by the PS5 build. Whatever causes the in-game failures is not a field-layout divergence.

## Live-device findings

All testing used Garlic Save Manager against a real PS5, converting real Steam saves via `ConvertPCToPS5` (drops the `HasID` field, re-encrypts, recomputes the checksum; body carried through byte-for-byte).

1. **First upload (autosave + profile + slot all converted together):** `"This extra save data is not compatible and cannot be used"`, then a crash attempting to start.
2. **Restoring the profile and slot save to native PS5 content, keeping only the converted autosave:** the "extra save data" error disappeared (isolating it to slot-type saves specifically, not the profile as first assumed) and the game reached the main menu — but **crashed on Continue** (loading the autosave).
3. **Size-matched control** (`data021Slot.bin`, PC 1104B vs PS5 1112B — nearly identical, unlike the autosave's 4x size difference): still produced "extra save data is not compatible", ruling out container/allocation size as the cause. Also revealed the error is a *pre-check* (no crash, just a rejection dialog) distinct from the autosave's *post-load* crash — two different validation paths for two different save types.
4. **Round-trip audit** (prompted by re-reviewing rather than more device testing): found and fixed the slot-ID truncation bug described above. All 8 real files (5 PC, 3 PS5) then round-tripped byte-identically through `Decode`→`Build`, proving the container writer itself was correct — before this, 6 of 8 mismatched.
5. **Re-tested with the fix:** still crashed on Continue.
6. **Patch version investigation:** PC's Steam build (`11636119`) was deployed 2023-08-14 per SteamDB. PS5's patch history (via prosperopatches.com's internal API) showed 4 patches, latest `01.000.003` also imported **2023-08-14** — the same day, strongly suggesting a simultaneous cross-platform release — but the PS5 was still on `01.000.002` (2022-08-30), a full patch behind.
7. **User updated the PS5 to 1.0.3** and replaced the PS5 autosave with a fresh Claire save (matching the PC save's character, eliminating that as a variable). Re-converted and re-uploaded under matched versions: **still crashed on Continue.**
8. **Account mixup caught by the user:** all uploads to that point had gone to the "Modded" PS5 account (`1ea2f4da`) via a hardcoded Garlic save index, not the "User1" account (`1ea2f4d9`) actually being used to test. Indices are positional in Garlic's API and shift as saves are added/removed — a real process bug, not a format one. `garlic.Client.MountByName` (already used elsewhere in this project, e.g. BG3) matches by title+name+uid specifically to avoid this; the RE2 investigation used raw indices instead and got bitten by it. Corrected and re-confirmed the target account/index before continuing.
9. **Retested on the correct account (User1), matched version:** still crashed on Continue.
10. **Matched-pair test:** converted *both* the autosave and the global profile from the same PC source (rather than a converted autosave paired with a native PS5 profile that has no record of the PC save's unlocks/progress) — reasoning that a mismatched profile/autosave pair could itself cause a load failure. Result: **crashed at game start**, before even reaching the main menu — worse than before, and implicating the profile file specifically. Reverted to the native (1.0.3-written) profile, restoring the game to working order.

## What's actually blocking

Three independent validation paths — slot-save pre-check, autosave post-load, and profile-at-startup — all reject PC-authored content on a version- and character-matched, byte-correct container whose field schema is now *known* to be identical between platforms.

Ruled out: patch/build version mismatch, container/block allocation size, character mismatch, the slot-ID truncation bug (real, fixed, but not sufficient alone), PS5 keystone/signing (`sce_sys/keystone` is tied to the game package at build time, identical for every save of a given build, and never touched by this tool), and — now — RSZ schema divergence.

That leaves the cause somewhere outside both the container and the field data. The most plausible remaining candidate is the PS5's own save-registration state: `sce_sys/param.sfo` carries a 1024-byte `PARAMS` blob whose first 32 bytes look like a digest. A quick check showed it is not a plain SHA-256 of any individual `.bin` file, which rules out only the simplest form — a keyed or whole-container digest remains possible, and `param.sfo` cannot be rewritten anyway without breaking the OS-level registration (proven during the BG3 work, see `docs/bg3.md`). If the console validates saves against state that Garlic's file-level writes cannot reach, this approach cannot work for RE2 regardless of how correct the file is.

Notably, this is the same wall the BG3 investigation hit from the opposite direction — there the PS5 accepted a grafted save with only a non-fatal "tampering" warning, whereas RE2 refuses outright.

## Repo state

- `internal/reengine/` is new and reviewed: 22 tests in the default build, 26 with `-tags reengine_rsz` (`rsz.go` now has its own test coverage, including a regression test for the alignment-base bug), all passing.
- `go.mod`/`go.sum` gained `golang.org/x/crypto` (pinned to v0.31.0, not latest, to stay on Go 1.22 — see `go.mod`) for the Blowfish primitive, and `gopkg.in/yaml.v3` (from the earlier, unrelated ludusavi-manifest work).
- Nothing here is wired into `games/`, the CLI, or the UI. `internal/reengine` has no callers outside its own package and its own tests.
- The PS5's "Modded" account (`1ea2f4da`) currently has a converted PC autosave in `data000.bin` (idx varies — resolve by name/uid, not a cached index) left in place from testing; a native backup exists in this session's temp directory if it needs restoring. The "User1" account's files are all back to native/working state.
