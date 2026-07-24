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

- `internal/engine`: the `Engine` interface (`Name`, `ParseConfig`, `Images`, `Compatibility`, `ConvertFromPS5`, `ConvertToPS5`, `InstallOutputs`) and a name → `Engine` registry (`Register`/`Get`). `Config` is `any`: each engine parses its own `engine_config` block and type-asserts it back internally, so different engines can have completely different config shapes.
- `internal/engine/unreal`: the only implemented engine. Generalizes what used to be `internal/games/clair`: `Config` carries the Unreal module name, the list of Garlic save images (each with its own `payload` filename — there's no single game-wide `PayloadName()` anymore), and a `class_equivalence` table of known-good `(pc class, ps5 class)` pairs per logical image. A class mismatch with no matching row currently only warns; it doesn't block conversion yet.
- `internal/engine/larian`: a stub. `"engine": "larian"` resolves to a real `Engine` and fails clearly ("not implemented yet") rather than "unknown engine" — the LSPK reader/writer for Baldur's Gate 3 hasn't been built.
- `internal/gvas`: unchanged low-level GVAS binary parsing/envelope-graft library (`Parse`, `ConvertWithEnvelope`), used by `internal/engine/unreal` rather than by a game package directly.
- `internal/games/registry.go` wires it together: `Profiles()` loads `games/*.json` (embedded defaults merged with `--games-dir` overrides, see below), and `ResolveEngine(profile)` looks up `profile.Engine` in the registry and calls its `ParseConfig(profile.EngineConfig)`. `SelectProfile(...)` returns a `Selected{Profile, Engine, Config}` bundle that `bridge.go` calls directly — there's no per-game Go code in that path at all.

### Adding a new Unreal game

1. Add `games/<key>.json` with `"engine": "unreal"` and an `engine_config` block, following `games/clair.json` as the template: `module`, an `images` list (each entry needs `logical`, `save_name`, `pc_file`, and `payload`), and a `class_equivalence` list for any logical image whose PC and PS5 save classes differ (an image with no row isn't class-checked at all, and identical classes on both sides never need a row).
2. `games/*.json` is embedded into the binaries via `//go:embed` in the root `embed.go` (`Builtin`), so a rebuild is required to pick up a new file added here. On-disk files under `--games-dir` (default `games`, resolved against cwd) override/extend the embedded ones by `game` key, and are edited in place without a rebuild; the first time that directory doesn't exist, `save-sync`/`save-sync-ui` seed it from the embedded defaults (best effort, never overwrites an existing directory or file).
3. Add tests under `internal/engine/unreal` exercising the new profile's `engine_config` the same way `unreal_test.go`'s `clairLikeConfig()` does — no new Go package needed unless the game needs logic the generic engine doesn't have yet.
4. Update `docs/<game>.md` and `README.md` current support as before.

### Adding a new engine

A genuinely new save format (e.g. Larian's LSPK, once implemented) needs a new `internal/engine/<name>` package implementing `engine.Engine`, registered in `internal/games/registry.go`'s `init()`. See `internal/engine/larian` for the minimal shape of a not-yet-implemented engine.
