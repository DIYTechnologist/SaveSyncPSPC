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

Conversion logic (container/cipher decode-encode, RSZ/GVAS/LSPK/TLV field handling, the `engine.Engine` registry) lives in [`savesync-engine`](https://github.com/DIYTechnologist/savesync-engine), a standalone Go module this repo imports (`github.com/DIYTechnologist/savesync-engine`) rather than owns. A "game" is a `games/<key>.json` profile (embedded in that module, overridable/extendable via `--games-dir`) naming an engine and supplying that engine's config; there is no per-game Go plugin to write, and this repo has no engine-level Go code at all - `internal/bridge` calls straight into the library's `games.SelectProfile`/`Selected{Profile, Engine, Config}` and the returned `engine.Engine`'s `ConvertToPS5`/`ConvertFromPS5`/`Inspect`/`InstallOutputs`.

**See `savesync-engine`'s own `docs/dev.md`** for the full engine architecture (the `Engine` interface, per-engine breakdown, dynamic image resolution, adding a new game/engine) and the portability gate. Per-format deep dives live there too:

- [`docs/dev-res2.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/dev-res2.md) / [`docs/ressave.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/ressave.md) - Resident Evil 2 / RE Engine DSSS container, RSZ, the eboot.bin disassembly.
- [`docs/dev-res4.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/dev-res4.md) - RE4's "Lime" cipher and key discovery.
- [`TODO.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/TODO.md) - the wider RE Engine family (RE3/RE4/RE7/Village/Requiem) findings, and The Alters (`elbsave`) investigation.
- [`docs/bg3.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/bg3.md) - Baldur's Gate 3's LSPK format, transport lessons, and the PS5→PC hang investigation.
- [`docs/subnautica.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/subnautica.md) - Subnautica's gzip+TLV container.
- [`TestCases.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/TestCases.md) - format/cipher-level test status for every supported game and direction.

What stays in *this* repo: the CLI/UI (`cmd/`, `internal/ui`), the Garlic HTTP transport (`internal/garlic`), and orchestration around the library (`internal/bridge` - backup, dynamic-image resolution driven by `--ps5-save-name`/`--steam-id`, applying converted output back through Garlic). Adding a new game on an already-supported engine, or a genuinely new engine, means changing `savesync-engine`, then bumping this repo's `go.mod` `require` to pick it up - see that module's own docs for the how-to.
