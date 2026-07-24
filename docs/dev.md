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
                                             (GVAS, implemented)    (LSPK, not yet implemented)
```

- `internal/engine`: the `Engine` interface (`Name`, `ParseConfig`, `OverrideTokens`, `Images`, `Compatibility`, `Inspect`, `ConvertFromPS5`, `ConvertToPS5`, `InstallOutputs`) and a name → `Engine` registry (`Register`/`Get`). `Config` is `any`: each engine parses its own `engine_config` block and type-asserts it back internally, so different engines can have completely different config shapes. Also defines the portability-gate vocabulary shared across engines: `Tier` (`TierConvertible` / `TierBlocked` / `TierWrongFormat`), `Side` (`SidePC` / `SidePS5`), `CheckResult`, and `Verdict`.
- `internal/engine/unreal`: the only implemented engine. Generalizes what used to be `internal/games/clair`: `Config` carries the Unreal module name, the list of Garlic save images (each with its own `payload` filename — there's no single game-wide `PayloadName()` anymore), and a `class_equivalence` table of known-good `(pc class, ps5 class)` pairs per logical image.
- `internal/engine/larian`: a stub. `"engine": "larian"` resolves to a real `Engine` and fails clearly ("not implemented yet") rather than "unknown engine" — the LSPK reader/writer for Baldur's Gate 3 hasn't been built.
- `internal/gvas`: unchanged low-level GVAS binary parsing/envelope-graft library (`Parse`, `ConvertWithEnvelope`), used by `internal/engine/unreal` rather than by a game package directly.
- `internal/games/registry.go` wires it together: `Profiles()` loads `games/*.json` (embedded defaults merged with `--games-dir` overrides, see below), and `ResolveEngine(profile)` looks up `profile.Engine` in the registry and calls its `ParseConfig(profile.EngineConfig)`. `SelectProfile(...)` returns a `Selected{Profile, Engine, Config}` bundle that `bridge.go` calls directly — there's no per-game Go code in that path at all.

### Adding a new Unreal game

1. Add `games/<key>.json` with `"engine": "unreal"` and an `engine_config` block, following `games/clair.json` as the template: `module` (leave `""` unless the game's SaveGame classes are native `/Script/...` classes, not Blueprints - see the portability gate section below), an `images` list (each entry needs `logical`, `save_name`, `pc_file`, and `payload`), and a `class_equivalence` list for any logical image whose PC and PS5 save classes differ (an image with no row isn't class-checked at all, and identical classes on both sides never need a row). `pc`/`ps5` in each row are matched by class-name suffix, not full path - see `games/clair.json`'s note for why.
2. `games/*.json` is embedded into the binaries via `//go:embed` in the root `embed.go` (`Builtin`), so a rebuild is required to pick up a new file added here. On-disk files under `--games-dir` (default `games`, resolved against cwd) override/extend the embedded ones by `game` key, and are edited in place without a rebuild; the first time that directory doesn't exist, `save-sync`/`save-sync-ui` seed it from the embedded defaults (best effort, never overwrites an existing directory or file).
3. Add tests under `internal/engine/unreal` exercising the new profile's `engine_config` the same way `unreal_test.go`'s `clairLikeConfig()` does — no new Go package needed unless the game needs logic the generic engine doesn't have yet.
4. Update `docs/<game>.md` and `README.md` current support as before.

### Adding a new engine

A genuinely new save format (e.g. Larian's LSPK, once implemented) needs a new `internal/engine/<name>` package implementing `engine.Engine`, registered in `internal/games/registry.go`'s `init()`. See `internal/engine/larian` for the minimal shape of a not-yet-implemented engine.

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
