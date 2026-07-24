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

## Adding a New Game

Each supported game needs metadata plus a registered Go implementation.

1. Add metadata under `games/`.

Example:

```json
{
  "game": "mygame",
  "name": "My Game",
  "ids": [
    {
      "id": "PPSA00000",
      "region": "EU"
    }
  ]
}
```

2. Add a package under `internal/games/<game>/`.

The package must implement `gameapi.Game`:

```go
type Game struct{}

func (Game) Key() string
func (Game) Name() string
func (Game) TitleIDs() []string
func (Game) PayloadName() string
func (Game) SaveImages() []gameapi.SaveImage
func (Game) Compatibility() gameapi.Compatibility
func (Game) ConvertFromPS5(map[string][]byte, string) (gameapi.ConversionResult, error)
func (Game) ConvertToPS5(string, map[string][]byte) (gameapi.ConversionResult, error)
func (Game) InstallOutputs(map[string][]byte, string, string) error
```

3. Register the game in `internal/games/registry.go`.

```go
var registry = map[string]gameapi.Game{
    "clair":  clair.Game{},
    "mygame": mygame.Game{},
}
```

4. Define required save images.

Each `SaveImage` maps a Garlic save image to a PC save file:

```go
gameapi.SaveImage{
    Logical:  "gameplay",
    SaveName: "sdimg_EXAMPLE",
    Label:    "Example",
    PCFile:   "Example.sav",
}
```

The UI only shows complete groups where every required image is present for the same user/title ID.

5. Implement conversion.

If the game uses Unreal GVAS saves and can use envelope grafting, reuse `internal/gvas`. If the game needs a different format, keep that parser/converter inside the game package or a shared internal package if another game will reuse it.

6. Add tests.

Minimum expected tests:

- Metadata maps title IDs correctly.
- Supported grouping requires every needed save image.
- Missing PC save files fail before conversion.
- PS5 to PC conversion writes every expected output.
- PC to PS5 conversion writes every expected replacement payload.
- Backup layout remains unchanged:

```text
backup/<game>-<yyyymmddhhmmss>/
  PC/
  PS5/
```

7. Update docs.

Add `docs/<game>.md` with:

- Supported title IDs and regions.
- Required Garlic save images.
- Required PC save files.
- Compatibility/version notes.
- Known limitations and restore guidance.

Update `README.md` current support if the game is user-ready.
