# Baldur's Gate 3 — Investigation Notes and Known Issues

Status of the `internal/engine/larian` (LSPK format) work: **PC → PS5 conversion is confirmed working in-game.** **PS5 → PC conversion is broken** — every save tested hangs at 0% on load, regardless of its origin, and the root cause is unresolved. This document covers the format facts, what's confirmed, what's broken, and what a fix would need. BG3 is a supported, wired-in game (`games/bg3.json`, `internal/engine/larian`, reachable from the CLI and UI) — this is a status/issues writeup for that existing engine.

## The container format ("LSPK")

Independently reverse-engineered from real PC and PS5 saves this session (format facts cross-referenced against [LSLib](https://github.com/Norbyte/lslib) for confirmation only — no code ported, consistent with this project's stance on the `ps5-save-converter` reference tool too).

```
40-byte header:
  "LSPK"              magic
  u32 = 18            version (only 18 supported)
  u64                 fileListOffset
  u32                 fileListSize
  u8                  flags
  u8                  priority
  [16 bytes]          md5
  u16                 numParts (only 1 supported)

File list (spans exactly to end-of-file - fileListOffset + fileListSize == len(data) on every real sample):
  u32                 numFiles
  u32                 compressedSize
  [raw LZ4 block]     decompresses to numFiles * 272-byte entries:
    name[256]           null-terminated
    offsetLo u32 + offsetHi u16   (48-bit file offset)
    u8 part
    u8 flags            low nibble = compression method: 0 none, 1 zlib, 2 lz4
    u32 sizeOnDisk
    u32 uncompressedSize

Entries are 64-byte-aligned (0xAD padding) from the end of the header.
Per-entry content compression: zlib on every entry observed on both platforms.
```

### Three writer requirements, each found by silent in-game rejection

Building a *modified* archive (as opposed to just parsing/repacking an unchanged one) needs an LZ4 *encoder*, and needs it to be exactly right — each of these was invisible until tested live on a real PS5:

1. **64-byte entry alignment.** Matches LSLib's `PackageWriter.WritePadding`. An unaligned-but-otherwise-valid archive is silently invisible to the game — no error, it just doesn't load.
2. **The file-list table's LZ4 must actually compress.** A literal-only "encoding" (output ≥ input) was rejected. `encodeLZ4Block` is a real greedy match-finding raw-block encoder.
3. **The header `md5[16]`.** MD5 over every entry's *uncompressed* content, concatenated in *physical* (on-disk offset) layout order — not file-list/table order — then every output byte incremented by 1 (mod 256). The physical-order requirement was pinned empirically: a real save's own stored MD5 was used as an oracle against 12 algorithm variants (order × content-stage × with/without the +1 step); exactly one matched.

## Transport lessons (Garlic, not the format)

Three bugs, all upstream of the file format, that silently corrupted every early upload attempt:

1. **Never write anything under `sce_sys/`.** The PS5 tracks saves in an OS-level savedata database, separate from the raw files Garlic exposes. Writing `sce_sys/param.sfo` into a mounted container breaks its registration — confirmed by causing a duplicated phantom entry (`sdimg_sdimg_Save0002`) and the save vanishing from the in-game list. Garlic also scrubs `param.sfo` on export (zeroes `ACCOUNT_ID`, rewrites `SAVEDATA_DIRECTORY` to its own image filename), so even round-tripping an exported one back in mis-registers the container.
2. **Garlic's HTTP API decodes `%20` but not `+` as a space.** Go's `url.Values.Encode()` uses `+`. Every request for a space-containing filename (all real BG3 save names) silently touched a wrong, literally-`+`-named file — self-consistently across upload and download-verify, which is what made it hard to spot. Fixed in `garlic.Client.endpoint`.
3. **Garlic zeroes `param.sfo` on mount reads too** (`"sfo_zeroed": true`), so container metadata can't be inspected through this transport at all — a real constraint, not a bug, but worth knowing before trying to read account/region info this way (contrast with `docs/ressave.md`'s RE2 investigation, where Garlic reported `sfo_zeroed: false` and full `param.sfo` contents were readable).

## Architecture: dynamic image resolution

Unlike Clair (fixed `SaveName`/`Payload`/`PCFile` known from `games/clair.json`), BG3 has none of that: the PS5 save slot (`sdimg_Save0001` vs `sdimg_Save0002`...), the PS5 image's `.lsv` filename, and the PC `.lsv` filename are all per-instance, not config-known.

- `gameapi.SaveImage` carries `DynamicSaveName`/`DynamicPayload`/`DynamicPCFile` booleans; `games/bg3.json` sets all three.
- `bridge.go`'s `resolveDynamicImages` runs before any backup/conversion step: `DynamicSaveName` comes from the `--ps5-save-name` CLI flag (no way to infer it automatically); `DynamicPayload` is resolved by mounting the now-known slot and calling `engine.Engine.ResolvePayload` (finds the single `.lsv` among the mounted files); `DynamicPCFile` is resolved by `ResolvePCFile` (finds the single `.lsv` in the PC save directory), only for `pc-to-ps5`.
- `save-sync inspect` explicitly refuses dynamic-image games rather than attempting something misleading — it hasn't been wired into this resolution pass.

## Confirmed working: PC → PS5

A real 39-hour PC (Steam) save ("Ruined Battlefield") was rebuilt and uploaded to a real PS5 twice, at two points in the project's evolution:

1. **Manual recipe**, before the generic engine wiring existed: `Build()` with the three writer requirements above, `SaveInfo.json`'s `"Platform"` field rewritten `"Steam"` → `"Prospero"` via targeted regex (preserves BG3's own formatting — not a JSON re-marshal), uploaded to the PS5's existing container filenames. **Loaded correctly in-game** — right character, right state — with one non-fatal "tampering/corruption" warning, plausibly because the PC save's game version (`4.1.1.3905231`) was newer than the PS5 build (`4.1.1.3877533`) at the time.
2. **Full CLI**, after the generic wiring: `save-sync --game bg3 --ps5-save-name sdimg_Save0002 pc-to-ps5 --pc-dir ... --apply --yes` — no manual scripts, dynamic resolution discovering the real filenames via mount+list. Produced a manifest with correct party/level data and `"Platform": "Prospero"`, and `ReplacePayload` succeeded without error. This exercises the identical graft mechanism already confirmed working in (1), but **this specific CLI-driven upload's in-game load was never separately confirmed** — the conversation moved on to other work before that check happened. Worth a quick confirmation before treating the CLI path as fully proven, though there's no structural reason to expect it differs from (1).

Known gaps in this direction, not yet hit in practice: only one save image per BG3 profile is supported (`len(images) != 1` errors); the `.WebP` thumbnail and the OS-level save-list display name/art aren't updated by a graft, so the list may show stale text/art until the game next saves into that slot.

## Broken: PS5 → PC

Every save tested hangs at 0% load on the PC client, with no error dialog — and critically, **this happens identically regardless of the save's origin**:

1. A genuinely PS5-native save (`AutoSave_0`, the opening tutorial autosave) — both the raw, completely untouched download from Garlic, and the same file run through `ConvertFromPS5`/`rebuildWithPlatform` — hung identically. Since the raw, unmodified file *also* hangs, this rules out `Build()`/the rewrite mechanism as the cause for this file.
2. Content that's actually **Steam-native underneath** — the same "Ruined Battlefield" PC save that loaded fine on PS5 in the "confirmed working" section above, downloaded back and converted PS5→PC — hung the same way. Since this content's `StorySave.bin`/`Globals.lsf`/etc. bytes are genuinely PC-original (only the `Platform` field and container repacking touched it, twice), this rules out "genuinely PS5-authored content is incompatible" as the sole explanation — the hang isn't specific to native-PS5 data.

Both failure modes being identical points at something **outside the LSPK/JSON content entirely** — most likely BG3 maintaining its own save-recognition mechanism separate from just scanning the `Story/` directory for folders, the same class of problem as the PS5 OS-level savedata database issue documented above, just on the PC client side instead.

A black thumbnail was observed on `AutoSave_0` in both the raw and converted cases — almost certainly not a bug (that autosave was captured during the opening nautiloid-crash cutscene, largely a black screen) and not correlated with the hang, since it appeared identically whether or not our tool touched the file.

### The one diagnostic that was proposed but never run

Duplicate a real, definitely-working save folder (e.g. the user's actual `Ruined Battlefield - 39h 05m` folder) **wholesale, completely untouched by this tool**, into a brand-new folder name under `Story/`, and try loading *that*. This isolates the last variable:

- If the untouched duplicate **also** hangs → the hang has nothing to do with PS5 conversion, or even this tool, at all. It's about how BG3 recognizes manually-added save folders — proving the "own save registry" theory — and any fix would need a completely different approach (an actual save-import mechanism, not a file drop).
- If the untouched duplicate **loads fine** → folder duplication itself is fine, and the problem really is something in the conversion/repack path, even though the previous test (2) argued against that. Would need re-opening why a byte-preserved graft behaves differently from a truly untouched copy.

This test is cheap (no code, no upload, just a filesystem copy) and would immediately halve the remaining hypothesis space. It's the natural next step if this investigation resumes.

## What a real fix needs

Beyond running the diagnostic above: if the "own save registry" theory holds, resolving it needs understanding how BG3 actually recognizes a save as loadable — likely something written by the game itself outside the per-save folder (a profile-level index, a `.lsx`/`.lsf` catalog file, or similar), which this investigation never located. That's a materially different, and likely larger, problem than anything in the LSPK format work — closer in shape to needing an LSOF (`meta.lsf`) or Osiris (`StorySave.bin`) parser than to a container-format fix, and explicitly out of scope for what's been built so far.
