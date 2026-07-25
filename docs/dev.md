# Development Notes

This project is a Go application with two binaries:

- `save-sync`: CLI bridge for conversion, backup, install, and apply flows.
- `save-sync-ui`: local browser UI and JSON API.

## Local Development

Run formatting, tests, and host build:

```sh
make all
```

Run individual targets:

```sh
make fmt
make test
make build
```

Build host binaries:

```sh
make build
```

Outputs:

```text
bin/save-sync
bin/save-sync-ui
```

Embed and check a version string:

```sh
make build VERSION=2026.0724.120000
./bin/save-sync --version
./bin/save-sync-ui --version
```

Run the UI locally:

```sh
make ui
```

Open:

```text
http://127.0.0.1:8765/
```

## Release Builds

Build release binaries with a local Go toolchain:

```sh
make release
```

Build release binaries through Docker or Podman:

```sh
make docker-release
```

Release output:

```text
dist/linux-amd64/save-sync
dist/linux-amd64/save-sync-ui
dist/linux-arm64/save-sync
dist/linux-arm64/save-sync-ui
dist/windows-amd64/save-sync.exe
dist/windows-amd64/save-sync-ui.exe
```

Export Linux binaries from the runtime Docker image:

```sh
make docker-bin
```

Build the runtime Docker image:

```sh
make docker-build
```

Docker builds also accept `VERSION`:

```sh
make docker-build VERSION=2026.0724.120000
docker run --rm save-sync-ps-pc:latest save-sync --version
```

## CI/CD

CI workflow: `.github/workflows/ci.yml`

- Runs on pull requests.
- Runs on pushes to `master`.
- Checks formatting with `make fmt` and `git diff --exit-code`.
- Runs `make test`.
- Runs `make release`.
- Uploads `dist/` as a 1-day GitHub Actions artifact.
- On successful `master` push, creates a UTC timestamp tag:

```text
yyyy.mmdd.hhmmss
```

Release workflow: `.github/workflows/release.yml`

- Manual only.
- Inputs: an existing tag (usually created by CI), and `release_type` (`prerelease` or `release`, defaults to `prerelease`).
- Checks out that tag.
- Runs tests.
- Builds release binaries.
- Packages assets:

```text
save-sync-ps-pc-linux-amd64.tar.gz
save-sync-ps-pc-linux-arm64.tar.gz
save-sync-ps-pc-windows-amd64.zip
save-sync-ps-pc-checksums.txt
```

- Creates a GitHub Release with generated notes, marked as a pre-release unless `release_type` is set to `release`.

## Engine Abstraction

Conversion logic is a property of the save-game *engine* (e.g. Unreal's GVAS format), not of any one title. A "game" is just a `games/<key>.json` profile naming an engine and supplying that engine's config; there is no per-game Go plugin to write. This replaced the old `gameapi.Game`-per-game model (see `internal/games/clair/` in history prior to the engine abstraction) so that adding a second title on an already-supported engine takes a JSON diff, not a Go file.

```
games/<key>.json ──► profile loader (internal/games) ──► engine registry (internal/engine)
                                                              │
                                                    ┌─────────┴─────────┐
                                                    ▼                   ▼
                                          internal/engine/unreal   internal/engine/larian
                                        (GVAS, fully implemented)  (LSPK, read path only)
```

- `internal/engine`: the `Engine` interface (`Name`, `ParseConfig`, `OverrideTokens`, `Images`, `Compatibility`, `Inspect`, `ConvertFromPS5`, `ConvertToPS5`, `InstallOutputs`) and a name → `Engine` registry (`Register`/`Get`). `Config` is `any`: each engine parses its own `engine_config` block and type-asserts it back internally, so different engines can have completely different config shapes. Also defines the portability-gate vocabulary shared across engines: `Tier` (`TierConvertible` / `TierBlocked` / `TierWrongFormat`), `Side` (`SidePC` / `SidePS5`), `CheckResult`, and `Verdict`.
- `internal/engine/unreal`: the only fully-implemented engine (read + convert + write). Generalizes what used to be `internal/games/clair`: `Config` carries the Unreal module name, the list of Garlic save images (each with its own `payload` filename — there's no single game-wide `PayloadName()` anymore), and a `class_equivalence` table of known-good `(pc class, ps5 class)` pairs per logical image.
- `internal/engine/larian`: read path only (LSPK container parsing + `Inspect`, see below). `"engine": "larian"` resolves to a real `Engine`; `ConvertFromPS5`/`ConvertToPS5`/`InstallOutputs` still fail clearly ("not implemented yet") since the actual conversion needs format work (LSOF, Osiris, mod-list parity) that hasn't been done.
- `internal/gvas`: unchanged low-level GVAS binary parsing/envelope-graft library (`Parse`, `ConvertWithEnvelope`), used by `internal/engine/unreal` rather than by a game package directly.
- `internal/games/registry.go` wires it together: `Profiles()` loads `games/*.json` (embedded defaults merged with `--games-dir` overrides, see below), and `ResolveEngine(profile)` looks up `profile.Engine` in the registry and calls its `ParseConfig(profile.EngineConfig)`. `SelectProfile(...)` returns a `Selected{Profile, Engine, Config}` bundle that `bridge.go` calls directly — there's no per-game Go code in that path at all.

### Adding a new Unreal game

1. Add `games/<key>.json` with `"engine": "unreal"` and an `engine_config` block, following `games/clair.json` as the template: `module` (leave `""` unless the game's SaveGame classes are native `/Script/...` classes, not Blueprints - see the portability gate section below), an `images` list (each entry needs `logical`, `save_name`, `pc_file`, and `payload`), and a `class_equivalence` list for any logical image whose PC and PS5 save classes differ (an image with no row isn't class-checked at all, and identical classes on both sides never need a row). `pc`/`ps5` in each row are matched by class-name suffix, not full path - see `games/clair.json`'s note for why.
2. `games/*.json` is embedded into the binaries via `//go:embed` in the root `embed.go` (`Builtin`), so a rebuild is required to pick up a new file added here. On-disk files under `--games-dir` (default `games`, resolved against cwd) override/extend the embedded ones by `game` key, and are edited in place without a rebuild; the first time that directory doesn't exist, `save-sync`/`save-sync-ui` seed it from the embedded defaults (best effort, never overwrites an existing directory or file).
3. Add tests under `internal/engine/unreal` exercising the new profile's `engine_config` the same way `unreal_test.go`'s `clairLikeConfig()` does — no new Go package needed unless the game needs logic the generic engine doesn't have yet.
4. Update `docs/<game>.md` and `README.md` current support as before.

### Adding a new engine

A genuinely new save format needs a new `internal/engine/<name>` package implementing `engine.Engine`, registered in `internal/games/registry.go`'s `init()`.

## Larian (Baldur's Gate 3) - Read Path

`internal/engine/larian/lspk.go` implements the LSPK save-container format: parse a real `.lsv`'s header and entry table, read a named entry's raw on-disk bytes or zlib-decompressed content, and `Repack` an unmodified archive back to bytes. This is a read path only - `ConvertFromPS5`/`ConvertToPS5`/`InstallOutputs` still return "not implemented yet". Real conversion needs an LSOF parser (`meta.lsf`), an Osiris parser (`StorySave.bin`), and mod-list parity checking against a save's Profile image (`modsettings.lsx`), none of which exist yet.

**Format facts, independently confirmed against real PS5 and PC Baldur's Gate 3 saves this session** (not ported from any other tool, per the licensing note in the original design spec):

- 40-byte header: `LSPK` magic, `version u32` (only `18` supported), `fileListOffset u64`, `fileListSize u32`, `flags u8`, `priority u8`, `md5[16]`, `numParts u16` (only `1` supported - refused rather than mis-parsed, since no multi-part sample exists to develop against).
- The file list always spans to end-of-file (`fileListOffset + fileListSize == len(data)` held exactly on both real samples) - `Parse` refuses to guess if that invariant doesn't hold.
- The file list section is `numFiles u32` + `compressedSize u32` + a **raw LZ4 block** (not the LZ4 frame format - no header/footer, just the token/literal/match sequence loop) decompressing to `numFiles * 272` bytes of fixed-size entries: `name[256]` (null-terminated), `offsetLo u32` + `offsetHi u16` (giving a 48-bit file offset), `part u8`, `flags u8` (low nibble is the compression method: `0` none, `1` zlib, `2` lz4 - every entry observed on both platforms used zlib), `sizeOnDisk u32`, `uncompressedSize u32`.
- Go's stdlib has no LZ4 decoder, so `lz4BlockDecompress` in `lspk.go` is a from-scratch implementation of the public, well-documented raw-block algorithm (no third-party dependency added).
- `Repack` doesn't need an LZ4 *encoder* at all for the "nothing changed" case the round-trip test proves: it re-serializes the header from its parsed fields and copies everything else (all file content, and the original file-list table's compressed bytes) through verbatim. That's byte-identical to the input by construction whenever no field actually changed - which was confirmed against both real files (8.7 MB PS5 save, 29.4 MB PC save): `Parse` → `Repack` → `bytes.Equal(repacked, original)` held exactly, for every entry's raw and decompressed content too.
- `SaveInfo.json`'s `Platform` field is confirmed `"Prospero"` on the real PS5 save and `"Steam"` on the real PC save - exactly the string this tool would need to rewrite for a real PS5→PC conversion, matching the original design spec's claim.
- `Inspect` currently only checks LSPK magic/version/part-count and that the four required members (`meta.lsf`, `SaveInfo.json`, `StorySave.bin`, `Globals.lsf`) are present - both real saves pass. The richer BG3-specific gate checks from the original design spec (`osiris-version`, `lsof-version`, `mod-parity`, `build-order`) need those not-yet-built LSOF/Osiris parsers.

### Modifying and rebuilding archives: `WithReplacedEntry`, `Build`, and the writer requirements

Once a real conversion needs to change a file's content, an LZ4 *encoder* becomes necessary for the entry table, since offsets and sizes shift. `WithReplacedEntry` patches one entry in an existing archive; `Build` assembles a brand-new archive from an arbitrary `EntrySpec` set (the tool for grafting a different save's whole file set onto a container). Three writer requirements were established the hard way - each was a silent in-game rejection until found:

1. **64-byte entry alignment**: every packed entry (and the file list) is padded with `0xAD` bytes to a 64-byte boundary measured from the end of the header (matches LSLib's `PackageWriter.WritePadding`; confirmed on every real save). An unaligned but otherwise-valid archive is silently invisible to the game.
2. **The entry table's LZ4 must actually compress**: a valid-but-literal-only LZ4 encoding (output ≥ input) was also rejected. `encodeLZ4Block` is a real greedy match-finding raw-block encoder; `encodeLZ4LiteralOnly` survives only as its small-input fallback and as a test-fixture helper.
3. **Header `md5[16]`**: MD5 over every file's **uncompressed** content concatenated in **physical layout order**, then **every output byte incremented by 1**. The uncompressed/+1 parts come from LSLib's `PackageWriter.ComputeArchiveHash` (format facts only - no code ported, consistent with the project's stance on the `ps5-save-converter` reference tool); the physical-order part was pinned empirically, using a real game-written save's stored header MD5 as an oracle against twelve algorithm variants - exactly one matched. `MD5Recompute` is the strategy that matters; `MD5Unchanged`/`MD5Zero` remain only as experiment tooling and should be dropped when the real `ConvertFromPS5`/`ConvertToPS5` land.

### Confirmed working end-to-end: PC -> PS5 graft (2026-07-25)

A real 39-hour PC (Steam) save was successfully loaded on a real PS5 by rebuilding it with `Build()`: all entries from the PC `.lsv` (meta.lsf, StorySave.bin, Globals.lsf, LevelCache/*, WebP), `SaveInfo.json`'s `Platform` rewritten `"Steam"` -> `"Prospero"` (a targeted regex field rewrite preserving all other formatting), uploaded into a PS5 container under that container's existing filenames. Character/state loaded correctly in-game.

**The transport rules matter as much as the file format.** Getting to that result surfaced three independent transport failures, all now understood:

- **The PS5 tracks saves in an OS-level savedata database** the game queries; Garlic's raw file writes don't touch registration. Writing `sce_sys/param.sfo` into a mounted container *breaks* its registration (Garlic scrubs `param.sfo` on export - `ACCOUNT_ID` zeroed, `SAVEDATA_DIRECTORY` rewritten to Garlic's image-file name, e.g. `sdimg_Save0002` instead of the real `Save0002` - so re-importing an exported `param.sfo` mis-registers the container, producing doubled-name phantoms like `sdimg_sdimg_Save0002` and hiding the save from the game). **Rule: never write anything under `sce_sys/`; only write into containers the game itself created.**
- **Garlic's HTTP API decodes `%20` but not `+` as a space in query strings.** Go's `url.Values.Encode()` uses `+`, so every request for a space-containing filename (all real BG3 save names) silently read/wrote a wrong, literally-`+`-named file - self-consistently across upload and download-verify, which masked it. Fixed in `garlic.Client.endpoint` (regression test in `internal/garlic/client_test.go`).
- **Garlic zeroes `param.sfo` in mounted reads too** (`"sfo_zeroed": true` in its mount response), so container metadata can't be inspected through this transport at all.

Also confirmed: Garlic's mount response and `/api/files` list a mounted container's files (name/dir/size) - useful for discovery; the save-list display name in-game comes from the OS registration (set at container creation), not from anything inside the `.lsv`, so a grafted save shows the container's original name until the game next saves into it; and loading the graft produces a **non-fatal warning** ("tampering/corruption") - plausibly because the PC save's game version (`4.1.1.3905231`) is newer than the PS5 build (`4.1.1.3877533`), the exact build-order asymmetry the original design spec flagged, though a digest stored in the OS-side `PARAMS` blob (unreachable through Garlic) hasn't been ruled out.

What remains for a real `larian.Engine` conversion implementation: encode the recipe above (game-created container required, `.lsv` + `.WebP` only, existing filenames, `Platform` rewrite, `Build` with `MD5Recompute`), plus the still-unbuilt LSOF/Osiris parsers if the mod-parity and build-order gate checks from the original spec are to block bad conversions up front rather than relying on the game's own warning.

## Portability Gate (Unreal)

Before any graft or write, `ConvertFromPS5`/`ConvertToPS5` run every applicable check against both sides of every required image (`internal/engine/unreal/gate.go`). Each check has a tier:

- **Tier 3 (`magic`)** - the payload isn't a GVAS save at all. Never overridable.
- **Tier 2** - a specific, named check failed. Overridable one at a time via `--allow <check>`:
  - `module` - the save class's Unreal module doesn't match `engine_config.module` (looks like a different game). Only meaningful for native `/Script/...` classes; leave `module` empty to disable this check for Blueprint-based SaveGame classes (`/Game/...`), which carry no reliable module-like signal - confirmed against Clair's real saves, whose two images live under two entirely unrelated `/Game/` content folders.
  - `account-id` - an embedded SteamID64 was found (account-bound save).
  - `account-props` - a property name suggests account binding (`steam`, `psn`, `account`, `uniquenetid`, `userid`, `epicid`).
  - `tail` - no `None\0` property-map terminator in the final 32 bytes (a trailer/checksum this tool can't reproduce may be present).
  - `package-version` - UE4/UE5 package versions differ (also settable per-profile via `allow_package_version_mismatch: true`, once verified safe).
  - `class-map` - the (PC class, PS5 class) pair for a logical image isn't in `class_equivalence` and isn't identical on both sides.

If any required image fails a non-overridden check, the **whole run aborts before any write** - a partial graft across a multi-image game is worse than none.

**`class_equivalence` has three states**, not two: a row with `"verified": true` passes silently; `"verified": false` (a candidate) passes but always emits a warning and is recorded in the manifest; no matching row blocks. This lets you commit a not-yet-play-tested mapping without needing `--allow` on every run - see `save-sync inspect --record`.

**Overrides** (`--allow CHECK[,CHECK...]`, repeatable or comma-separated; `--allow-all` for every tier-2 check at once) are the primary way to author a new game's `class_equivalence` table, not just a safety valve - you cannot learn a mapping works without grafting it once. Rules:
- An `--allow` token that didn't correspond to an actual failure is a loud warning (not an error) after the run - typoing the wrong check name should be obvious, not silent.
- An unknown token is a hard error listing the valid ones.
- `pc-to-ps5` rejects `--allow-all` outright and requires `--apply --yes` for any override at all - console writes carry extra ceremony.
- Every check result (including which ones were overridden or are unverified candidates) is written into `garlic_sync_manifest.json` under `plugin.gate`, so a save that turns out subtly broken can be traced back to what was bypassed.

**`save-sync inspect`** runs the gate read-only, writing nothing:

```sh
save-sync --garlic URL --game clair --ps5-uid UID inspect --pc-dir ./SaveGames --record
save-sync inspect --file ./SomeSave.sav
```

Without `--pc-dir`, only the pulled PS5 payload's single-payload checks run (no "other side" to compare, so no class-map/package-version). `--record` prints a ready-to-paste candidate `class_equivalence` row for any class-map miss it finds.

### Confirmed against real data

`save-sync inspect` has been run against a real Clair save on both sides (a real Steam PC save plus both PS5 users' saves), read-only, no conversion attempted. Everything now checks out:

- **Real save classes are full Blueprint content paths, not the `/Script/Sandfall.*` paths the class_equivalence table originally shipped with** (those were carried over from an illustrative example, never validated). Confirmed real classes:
  - `gameplay`: PC `/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V8.BP_SaveGameObject_V8_C`, PS5 `/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V7.BP_SaveGameObject_V7_C`.
  - `container`: PC and PS5 both `/Game/jRPGTemplate/Blueprints/SaveObjects/BP_jRPG_SavesContainer.BP_jRPG_SavesContainer_C` - byte-identical on both platforms.
  - `class_equivalence` now matches by class-name suffix rather than full path (restoring this tool's original, previously-working `strings.HasSuffix` behavior from before the engine refactor), and `games/clair.json`'s `module` is `""` since Blueprint classes carry no reliable module-like signal (`gameplay` and `container` don't even share a content folder).
- `account-id`, `account-props`, and `tail` all **passed cleanly** on both real PS5 saves and the real PC save - no false positives observed, for either user.
- `class-map` and `package-version` (the pairwise checks, which need both a real PC and a real PS5 payload) both passed cleanly on a real `--pc-dir` run for both images, both users. `games/clair.json` now has an explicit `verified: true` row for `container` too (rather than relying on the implicit identity-match), recording the confirmed real value in case a future game patch makes PC and PS5 diverge.

This doesn't mean every save state or every future game patch is covered - it's one save family, checked once. If a check ever false-positives on a save that used to work, `save-sync inspect` shows exactly which one and why, and `--allow <check>` gets you past it for that run.
