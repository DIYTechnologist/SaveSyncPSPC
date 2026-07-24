# Save Sync PS-PC

PC-side save bridge for moving supported game saves between Garlic Save Manager on PS5 and a local PC save directory.

The service discovers supported saves from Garlic, groups the save images that belong together, runs a game-specific conversion plugin, and can write the converted payloads back through Garlic. Garlic still owns the PS5 side of the process: mounting, decrypting, uploading payloads, and re-encrypting saves.

## Current Support

See [docs/supported_games.md](docs/supported_games.md) for supported games and platform compatibility.
See [docs/games/clair.md](docs/games/clair.md) for Clair-specific save names, compatibility notes, and workflow details.
See [docs/dev.mnd](docs/dev.mnd) for development, release, and new-game implementation notes.

## Requirements

- Go 1.22 or newer
- Garlic Save Manager running on the PS5 and reachable over HTTP
- Valid owned PS5 saves already visible in Garlic
- A local PC save directory for the target game
- Docker or Podman, optional

## Quick Start

Build the binaries from the repo root:

```sh
make build
```

Build release binaries for Linux and Windows:

```sh
make release
```

Build those same release binaries using Docker instead of a local Go toolchain:

```sh
make docker-release
```

Show CLI help:

```sh
./bin/save-sync --help
```

Show binary version:

```sh
./bin/save-sync --version
./bin/save-sync-ui --version
```

Start the browser UI:

```sh
./bin/save-sync-ui --host 127.0.0.1 --port 8765
```

Or use the Makefile:

```sh
make ui
```

Then open:

```text
http://127.0.0.1:8765/
```

Set the Garlic URL in the UI, for example:

```text
http://192.168.2.67:8082
```

The UI can load saves and users from Garlic, group supported saves, filter out unsupported save images, and warn when the detected save version does not match the known compatibility profile.

## CLI Usage

The CLI can auto-discover a supported game from Garlic saves, or you can select one explicitly with `--game clair` or `--title-id PPSA17599`.

Every real conversion run creates a backup before converting or applying changes:

```text
backup/<game>-<yyyymmddhhmmss>/
  PC/
  PS5/
```

The `PC/` directory contains copies of the current local PC save files. The `PS5/` directory contains the payloads downloaded from Garlic, grouped by Garlic save image. These files are not converted or uploaded; they are left as restore points. Use `--backup-root` to choose a different backup root.

Pull PS5 payloads from Garlic and create PC save files:

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game clair \
  --ps5-uid 1ea2f4d9 \
  ps5-to-pc \
  --pc-dir ./SaveGames \
  --output-dir ./converted_from_ps5 \
  --force
```

Install the converted PC files into the PC save directory after creating a backup:

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game clair \
  --ps5-uid 1ea2f4d9 \
  ps5-to-pc \
  --pc-dir ./SaveGames \
  --output-dir ./converted_from_ps5 \
  --install \
  --force
```

Convert local PC saves into PS5 replacement payloads without writing to the PS5:

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game clair \
  --ps5-uid 1ea2f4d9 \
  pc-to-ps5 \
  --pc-dir ./SaveGames \
  --output-dir ./converted_for_ps5 \
  --force
```

Apply converted payloads back to PS5 through Garlic:

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game clair \
  --ps5-uid 1ea2f4d9 \
  pc-to-ps5 \
  --pc-dir ./SaveGames \
  --output-dir ./converted_for_ps5 \
  --apply \
  --yes \
  --force
```

## Docker

Build the image:

```sh
make docker-build
```

Embed a specific version in Docker-built binaries:

```sh
make docker-build VERSION=2026.0724.120000
```

Show CLI help inside the image:

```sh
make docker-help
```

Run the UI in Docker:

```sh
make docker-ui
```

Export Linux binaries from the Docker-built image:

```sh
make docker-bin
```

Run the CLI with a mounted PC save directory:

```sh
docker run --rm --network host \
  -v "$PWD/SaveGames:/saves" \
  -v "$PWD/out:/out" \
  save-sync-ps-pc:latest \
  save-sync --garlic http://192.168.2.67:8082 --game clair \
  pc-to-ps5 --pc-dir /saves --output-dir /out --force
```

On platforms where Docker does not support `--network host`, publish the UI port with `-p 8765:8765` and make sure the container can reach the Garlic IP.

## Release Artifacts

`make release` and `make docker-release` create:

```text
dist/linux-amd64/save-sync
dist/linux-amd64/save-sync-ui
dist/linux-arm64/save-sync
dist/linux-arm64/save-sync-ui
dist/windows-amd64/save-sync.exe
dist/windows-amd64/save-sync-ui.exe
```

GitHub Releases package those binaries as:

```text
save-sync-ps-pc-linux-amd64.tar.gz
save-sync-ps-pc-linux-arm64.tar.gz
save-sync-ps-pc-windows-amd64.zip
save-sync-ps-pc-checksums.txt
```

## Game Discovery

Supported games are described by metadata under `games/` and Go implementations under `internal/games/`.

Each game has:

- A JSON metadata file, for example `clair.json`
- A registered Go implementation, for example `internal/games/clair`

The metadata maps one or more PS5 title IDs to a stable game key:

```json
{
  "game": "clair",
  "name": "Clair Obscur: Expedition 33",
  "ids": [
    {
      "id": "PPSA17599",
      "region": "EU"
    }
  ]
}
```

The game key is used to look up a registered Go implementation. A new supported game should add its own metadata, implement the `gameapi.Game` interface, register it in `internal/games/registry.go`, and add tests for discovery and required save grouping.

## Development

Run all checks:

```sh
make all
```

Run individual checks:

```sh
make fmt
make test
make build
make release
```

Clean generated caches and build artifacts:

```sh
make clean
```

CI runs on pushes to `master` and pull requests. Successful `master` builds upload a 1-day artifact and create a timestamp tag in `yyyy.mmdd.hhmmss` format. Releases are manual: run the Release workflow with one of those tags to create GitHub Release assets and generated release notes.

## Safety Notes

- Back up PC and PS5 saves before applying replacements.
- Save Sync PS-PC also creates `backup/<game>-<yyyymmddhhmmss>/PC` and `backup/<game>-<yyyymmddhhmmss>/PS5` before conversion.
- Disable Steam Cloud temporarily while testing converted PC files, otherwise cloud sync may overwrite local files.
- Do not point `--output-dir` at the PC save directory. The tool refuses some dangerous paths, but separate output directories are easier to inspect and recover from.
- `--apply --yes` writes replacement payloads back to PS5 through Garlic. Use the dry-run output first and verify the generated manifest.
- This project is not a PS5 save ownership, resigning, or decryption tool.
