# Resident Evil 2 (2019) Save Format — Investigation Notes

Research spike into whether `save-sync`'s PC↔PS5 bridge could be extended to Resident Evil 2 (Capcom's RE Engine), prompted by the user having both a PC (Steam) and PS5 copy with real saves on each.

**PC → PS5 conversion works: a real Steam save was converted and loaded successfully on a real PS5.** Getting there took four separate discoveries, the last two of which were only found by disassembling the game's own executable.

RE2 is now a wired-in game: `games/re2.json` selects `internal/engine/reengine`, which adapts the format work in `internal/reengine` to the `engine.Engine` interface, so `save-sync --game re2` works the same way `clair` and `bg3` do. The conversion library itself is verified to reproduce, byte-for-byte, the exact files that loaded on the console. **The CLI path has not yet had its own end-to-end run against a live console** (Garlic went offline before that could be done) - the library beneath it is confirmed, but the wiring around it is only unit-tested.

## What a working conversion requires

All four are necessary; each was a silent failure until found:

1. **Container rebuild** — DSSS header, Blowfish-LE/CBC cipher, murmur3 checksum. Byte-exact against every real save.
2. **Preserve the trailing slot number.** Blowfish covers only the 8-byte-aligned prefix of the body; the remaining 1-7 bytes are stored in the clear and hold the save's own slot id. Dropping them truncates the file.
3. **Re-serialize the field data with alignment recomputed for the destination body offset.** RSZ field alignment is computed against the body's *absolute file offset*. A PC body sits at `0x20` (16-aligned), a PS5 body at `0x18` (8 mod 16), so copying a body verbatim between them shifts every 16-byte-aligned field - including array headers - by 8 bytes. This was the root cause of the crashes.
4. **Retarget the platform fields.** Class `0x8b7dd7a1` carries `0xb41fa365` (Enum: PC `3` / PS5 `2`) and `0xe231b945` (Boolean: PC `true` / PS5 `false`). Without these the game parses the save fine and then refuses it as "not compatible".

The progression through the fixes was itself the clearest diagnostic - each one produced a distinctly different failure:

| State | Result in-game |
|---|---|
| Verbatim body copy | Hard crash (SIGSEGV, null write on `SaveThread`) |
| Body kept at `0x20` (ID field retained) | "Can't load the saved data because it is corrupted" |
| Re-aligned for `0x18`, native PS5 shape | "This save data is not compatible and cannot be used" |
| ...plus platform fields retargeted | **Loads** |

Confirmed twice: a 728 KB fresh autosave (`data000.bin` -> slot 0) and a 2.5 MB Dec-2020 manual save (`data001Slot.bin` -> slot 1), so the process is not specific to one save's size, age, or slot type. Convert each PC file into the PS5 container for **the same slot** - the trailing slot id must match the container it lands in.

### Known cosmetic limitation: stale save-select metadata

A converted save loads the correct game state, but the save-select / loading screen shows the *previous* occupant's text (area, goal, times-saved, character). That metadata lives in the PS5's OS-level save registration - `sce_sys/param.sfo`'s `DETAIL` and `SUBTITLE` fields - not in the save file, and Garlic's raw file writes do not update it. It should correct itself the next time the game saves into that slot.

Rewriting `param.sfo` to fix the text is **not** recommended: the BG3 investigation established that writing anything under `sce_sys/` breaks the container's OS-level registration (producing phantom duplicate entries and saves vanishing from the in-game list - see `docs/bg3.md`). That is a real risk of breaking a working container for a purely cosmetic gain.

## Status

| Direction | Status |
|---|---|
| Decode/re-encode a save (either platform) | Works, byte-exact |
| RSZ field parsing (both platforms) | Works — all real saves parse fully, schemas identical across platforms |
| RSZ re-serialization | Works — reproduces every real save's exact layout (padding bytes aside; see below) |
| **PC → PS5 conversion** | **Works — confirmed on a real PS5 with two different saves** (a small fresh autosave and a 2.5 MB deep-progress manual slot) |
| PS5 → PC conversion | Not attempted (should be the same process in reverse) |

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

**Every hypothesis checkable from outside the game binary has now been ruled out:**

- Patch/build version mismatch (matched, still crashed)
- Container/block allocation size (`SAVEDATA_BLOCKS` = 256 ≈ 8MB, far more than needed)
- Character mismatch (matched, still crashed)
- The slot-ID truncation bug (real, fixed, but not sufficient alone)
- PS5 keystone/signing (`sce_sys/keystone` is tied to the game package at build time, identical for every save of a given build, never touched by this tool)
- RSZ schema divergence (identical structure across platforms - see above)
- **`param.sfo`'s `PARAMS` blob as a content digest.** Pulled `param.sfo` from all three RE2 containers (`data000.bin`, `data021Slot.bin`, `data00-1.bin`) on both PS5 accounts used for testing. Within one account, `PARAMS` is *byte-identical* between `data021Slot.bin` and `data00-1.bin` (completely different files, different sizes) and differs from `data000.bin`'s value in exactly **2 bytes**, at fixed offsets, with the same two values across both accounts. That's the signature of a small structured field (almost certainly a save-type flag distinguishing the autosave from the slot/profile family, matching Sony's documented `sceSaveDataParam` shape) — not a hash, which would change throughout if content changed at all. This rules out the "unreachable per-save digest" theory that was the last standing hypothesis.

That closes out everything reachable through static Garlic-level inspection alone. Two further live tests below (a kitchen-sink field patch, and a real crash dump via kernel-log capture) went further still and confirm the same conclusion much more directly.

### A promising lead that turned out to be a dead end: the "platform flag" fields

Two fields inside a small, fixed top-level settings class (`0x8b7dd7a1`) showed a **perfectly consistent** split with zero exceptions across every sample checked (3 PC saves, 4 PS5 saves, both characters, multiple accounts):

| Field | PC (3/3) | PS5 (4/4) |
|---|---|---|
| `0xb41fa365` (Enum) | 3 | 2 |
| `0xe231b945` (Boolean) | true | false |

This is exactly the shape BG3's `"Platform": "Steam"/"Prospero"` field had - a value with no overlap at all between platforms, in a settings-shaped object present in every save. To test it, `internal/reengine/rsz.go` gained `Value.ValueOffset`/`ValueSize` (the byte range a scalar value occupies in the body), enabling a surgical patch - the same "rewrite one field, leave everything else untouched" approach used for BG3's `Platform` field, rather than a full RSZ re-serialization. A real PC save had exactly these 2 bytes changed (confirmed: a full body diff showed precisely 2 differing bytes out of 2,542,812, at the expected offsets) and was uploaded to a live PS5.

**It still crashed identically.** This is a genuine, informative negative result: the cleanest, most consistent signal found in the entire investigation is not the actual blocker.

### The last black-box test: patching every disjoint field at once

Rather than stop at one lead, every field across the whole body that showed zero value-overlap between platforms was collected and patched simultaneously - a deliberately blunt "kitchen sink" test. Of the 38 raw candidates, 8 were excluded as clearly not flag-like (values in the ~6×10¹⁷ range, consistent with .NET-style ticks - i.e. per-save timestamps, which will always differ between any two saves regardless of platform and would prove nothing if patched). The remaining 30 fields (33 occurrences, since some classes appear more than once in the tree) were patched to their most-common PS5-observed value in one pass - a 47-byte total change across a 2.5MB body, everything else byte-for-byte untouched. Uploaded and tested live.

**It also crashed identically.** This closes out black-box investigation for real: every field that differs measurably and consistently between a PC-origin and PS5-origin save, patched all at once, still doesn't produce a loadable save. Whatever the console/game validates at load time is not expressible as a save-data field value difference at all - it lives in logic the game executable runs, which is unreachable without disassembling the binary itself.

### Getting a real crash dump: `klogsrv` and the kernel log

The PS5 used for testing is jailbroken (required for Garlic itself), and exposes a kernel/syslog stream over TCP on port 3232 (`klogsrv`, a common homebrew tool in that ecosystem). Streaming this during a Continue attempt captures real `libSceSaveData` API tracing and, critically, a full crash dump when the game faults - a fundamentally cheaper way to get information than disassembling the binary, since it's just capturing debug output the system already produces.

Two independent crash captures, on two very different save contents (the original 39-hour save and a fresh/early save, see below), both show:

```
signal: 11 (SIGSEGV)
thread name: SaveThread
proc name: eboot.bin
reason: page fault (user write data, page not present)
fault address: 0000000000000000
rip: 0000000003095285          <- IDENTICAL in both captures
```

A null-pointer **write** crash on the game's own save-loading thread, at the **exact same instruction address** both times, despite the two saves differing enormously in content and size. This is the single cleanest fact in the entire investigation: the crash is not reacting to *what* is in the save - it triggers deterministically at a fixed point in the game's own code for *any* PC-origin/rebuilt save.

One early register match looked promising - `r14` in the first capture held `0x5507376f`, which is exactly a real field hash from class `0x3b9a2a09` (the same class from the alignment-bug work). But that hash turned out to occur **3,444 times** in the 39-hour save (a per-room "gimmick"/interactable-state field - large counts are simply what a late-game save has, not a bug), so a register coincidentally holding it isn't strong evidence by itself. The second capture confirmed this: `r14` held a completely unrelated value that doesn't match any known field hash, while every other detail of the crash (signal, thread, fault address, and especially `rip`) stayed identical. The register match was a coincidence, not a diagnosis.

### Testing volume/capacity as a distinct hypothesis

The `r14` coincidence still raised a real, previously untested hypothesis: not a wrong *value*, but too much *volume* - if the game allocates something sized around a typical/expected count and a 57x-larger late-game save exceeds it, a null-pointer write on the loading thread is exactly the failure mode you'd expect. This is a fundamentally different category from every prior test, which only checked field values, never field *counts*.

All three available real PC saves were similarly late-game (3,444-3,480 occurrences of the field in question), so testing this needed a genuinely early save. The user generated one directly on the PC (a fresh autosave, 728KB vs the original 2.5MB, 366 occurrences of the same field - within range of PS5's own native 60, not PC's 3,444). Converted and uploaded the same way as every other test.

**It crashed identically** - same signal, same thread, same fault address, same `rip` as both prior captures. This rules out volume/capacity as the cause as conclusively as the value-based hypotheses were already ruled out: a save with barely more of this data than PS5's own native content still fails exactly the same way.

### Breakthrough: reading the game's own code, and the alignment root cause

The user supplied a copy of the game's PS5 executable (`eboot.bin`, v01.003 — matching the console's patch level). Critically it is **decrypted**: the inner ELF sits at file offset `0x1a0`, and the text segment disassembles into coherent x86-64. The crash address maps in cleanly — text loads at `0x400000` per the crash dump while the ELF's own vaddr base is `0`, so `rip 0x3095285` is module offset `0x2C95285`.

Two things confirmed the binary corresponds to what actually crashed: the disassembly at that address is a well-formed function, and the crash dump's `rdi = 0` is exactly what `xor edi,edi` — two instructions before the reported `rip` — produces.

**What the code at the crash site actually does:**

```
309523a:  test rdi,rdi          ; is arg1 null?
3095244:  je   0x3095283        ; yes -> null branch
3095283:  xor  edi,edi          ; null branch: rdi = 0
3095285:  mov  rsi,rax          ; <- reported rip
3095288:  call 0x3094a40        ; call with rdi = NULL
```

And `0x3094a40` is **not** a deserializer - it is an assert/log routine: it does `cmp BYTE PTR [rcx],0x23` / `cmp [rcx+1],0x20` (testing for a `"# "` message prefix), loads a format string with `lea rsi,[rip+...]`, and calls a printf-style vararg function (`xor eax,eax` before the call). In a retail build the logger global is null, so the *error reporter itself* faults.

**The crash was therefore a symptom, not the cause** - the game had already detected a problem and was trying to report it. Every black-box test up to this point had been chasing the wrong thing.

Following the recursion frames from the backtrace (`0x3096673 / 0x3095942 / 0x3095b11`) led to `0x3095ac0`, an array-allocation helper with overflow guards, where frame `0x3095b11` sits in its size computation (`newCount = count + additional`, then a `UINT64_MAX / elemSize` overflow check, falling through to `xor r13d,r13d` - return NULL). So the deserializer was reading **garbage array counts**, allocation refused them, and the resulting null reached the assert path.

**Why the counts were garbage - the actual root cause.** This investigation had already established (see the alignment-base bug above) that RSZ field alignment is computed against the body's *absolute file offset*. A PC body sits at `0x20` (16-aligned); a PS5 body sits at `0x18` (≡ 8 mod 16). `ConvertPCToPS5` copied the body **verbatim** into a container where it now started 8 bytes off, so every 16-byte-aligned field inside it - including array headers - was misread. The fact was known; its implication for conversion was missed.

**Confirmed live.** Rebuilding a PC save while *keeping* the ID field (so the body stays at `0x20`, the offset it was serialized for) and uploading it produced a **completely different result: no crash.** Instead the game showed a graceful *"Can't load the saved data because it is corrupted"* dialog - meaning it parsed the container and body successfully, walked far enough to run its own validation, and rejected it cleanly. Setting the embedded ID to `0` changed nothing, so the rejection is triggered by the `HAS_ID` flag itself (native PS5 saves never set it), not by the ID value.

### The RSZ re-serializer (`internal/reengine/rszwrite.go`)

`WriteRSZObjects(objs, dataOffset, trailer)` re-emits a parsed tree laid out for a given body offset - writing a tree parsed at one offset back out at another is exactly what re-aligns it. Two subtleties were needed to make it faithful:

- **The declared size is not the consumed size.** The format stores an explicit size ahead of each value, and it does not always match the bytes the value occupies (a Boolean may be declared 4 but occupy 1, with the field's 4-byte alignment absorbing the rest). Re-emitting the consumed size corrupted both the stored size and the alignment after it. `Value.DeclaredSize` now carries the format's own value.
- **Empty strings have no terminator.** The reader trims one trailing NUL, so a non-empty string round-trips by re-appending it - but an empty string is stored as length `0` with no units at all. Blindly adding a terminator lengthened the body and shifted everything after it.

**Validation:** re-emitting each of the ten real saves at its own original base reproduces the original length exactly, with two files byte-identical and the rest differing only in padding. Padding cannot be reproduced byte-for-byte because the game's own writer leaves stale garbage there (values like `0x24924924` are common) rather than zeroing it; since the reader skips padding, this does not matter. Matching length with all field positions unchanged is the meaningful property, and it holds for every sample on both platforms.

## Using it

```sh
save-sync --garlic http://<ps5>:8082 --game re2 --ps5-uid <uid> \
  --ps5-save-name sdimg_SAVESERVICE-LINE-0-1Slot \
  pc-to-ps5 --pc-dir <steam save dir> --output-dir ./out --apply --yes --force
```

Which slots exist differs per player, so the target slot is named with `--ps5-save-name` (the same mechanism BG3 uses) rather than being fixed in the profile. The PC-side filename is *derived* from that slot rather than discovered: a PC save directory holds every slot's file side by side, and a save carries its slot number internally, so it must be written into the container for its own slot. The engine also cross-checks the source save's embedded slot number against the target container and refuses a mismatch.

The global profile/settings save (`data00-1.bin`, slot `-1`) is refused outright - converting it was observed to crash the game at startup.

## Remaining work

- **The CLI path needs one live end-to-end run.** The underlying library is confirmed byte-identical to saves that loaded on a console, but `save-sync --game re2 ... --apply` has not itself been exercised against a real PS5.
- **PS5 → PC** is implemented and unit-tested but never confirmed in-game. It also writes `0` as the embedded Steam account id, which the game may reject; if so it needs the real id of the account that will load the save.
- The **stale save-select metadata** (above) is unresolved by design, not oversight.
- The **reflection symbol table** is present in the executable's rodata (real names like `get_SyncTarget`, `via.movie.MovieManager.GCTarget`). Since RSZ hashes are murmur3 of these names, hashing that table would recover human-readable field names locally, without the multi-megabyte external schema dumps - useful for making sense of the two platform fields rather than treating them as magic constants.
