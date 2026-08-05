# Save Sync PS-PC

PC-side save bridge for moving supported game saves between Garlic Save Manager on PS5 and a local PC save directory.

The service discovers supported saves from Garlic, groups the save images that belong together, runs the save-format engine declared by that game's profile (see "Game Discovery" below), and can write the converted payloads back through Garlic. Garlic still owns the PS5 side of the process: mounting, decrypting, uploading payloads, and re-encrypting saves.

# Licensing

- Personal use - AGPL
- Commercial use - Contact for a License
- AI Model Training  - Contact for a License

## Current Support

See [docs/supported_games.md](docs/supported_games.md) for supported games and platform compatibility.
See [docs/games/clair.md](docs/games/clair.md) for Clair-specific save names, compatibility notes, and workflow details.
See [docs/dev.md](docs/dev.md) for development, release, and new-game implementation notes.

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

Before any of that, a portability gate checks every save image for things that would make a graft unsafe or meaningless (wrong game, account-bound save, unmapped save-class pair, etc.) and blocks the whole run if anything fails. Inspect a save without converting or writing anything:

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game clair \
  --ps5-uid 1ea2f4d9 \
  inspect \
  --pc-dir ./SaveGames \
  --record
```

If a check blocks a run you believe is actually fine, bypass just that check with `--allow <check>` (see `docs/dev.md`'s "Portability Gate" section for the full list of checks and the override rules).

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

### Baldur's Gate 3 (dynamic save slot)

BG3 saves have no fixed filenames — pass `--ps5-save-name` naming the Garlic `sdimg_SaveNNNN` slot to target (check Garlic's save list for the one you want):

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game bg3 \
  --ps5-uid 1ea2f4d9 \
  --ps5-save-name sdimg_Save0002 \
  pc-to-ps5 \
  --pc-dir "./SaveGames/Ruined Battlefield - 39h 05m" \
  --output-dir ./converted_for_ps5 \
  --apply \
  --yes \
  --force
```

`--pc-dir` should point at the folder containing that save's single `.lsv` (and its matching `.WebP`), same as a real Steam BG3 save folder. `save-sync inspect` doesn't support BG3 yet (dynamic-image engines aren't wired into it) — errors clearly rather than doing something misleading.

### Resident Evil 2 (dynamic save slot)

Like BG3, RE2 save slots aren't fixed — pass `--ps5-save-name` naming the Garlic slot to target (the PC-side filename for that slot is derived automatically, since one PC save directory holds every slot side by side):

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game re2 \
  --ps5-uid 1ea2f4d9 \
  --ps5-save-name sdimg_SAVESERVICE-LINE-0-1Slot \
  pc-to-ps5 \
  --pc-dir "~/.local/share/Steam/userdata/<id>/883710/remote/win64_save" \
  --output-dir ./converted_for_ps5 \
  --apply \
  --yes \
  --force
```

PC → PS5 is confirmed working in-game. PS5 → PC is implemented and unit-tested but not yet confirmed in-game. The global profile/settings slot (`data00-1.bin`) is refused outright — converting it crashed the game at startup. See `docs/ressave.md` for the full format writeup.

### Subnautica (fixed save slot)

Subnautica's PC save lives inside the game's own install directory rather than under `AppData` — point `--pc-dir` at the `SavedGames` folder itself, not a specific slot (the profile picks the slot subdirectory):

```sh
./bin/save-sync \
  --garlic http://192.168.2.67:8082 \
  --game subnautica \
  --ps5-uid 1ea2f4d9 \
  ps5-to-pc \
  --pc-dir "~/.local/share/Steam/steamapps/common/Subnautica/SNAppData/SavedGames" \
  --output-dir ./converted_from_ps5 \
  --force
```

No encryption, no proprietary class/versioning system — the simplest format this tool handles. `subnautica_below_zero` uses the same engine and works the same way, except its PC/PS5 slot-number pairing is hardcoded per-profile rather than matching by number (see `docs/subnautica.md`). Not yet tested: loading a converted save in-game for either title.

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

Supported games are described entirely by metadata under `games/` — a game is a JSON profile naming a save-format *engine* (`internal/engine/unreal` for Unreal's GVAS format; `internal/engine/larian` for Baldur's Gate 3's LSPK format; `internal/engine/reengine` for Capcom RE Engine's DSSS format; `internal/engine/unityblb` for Subnautica's gzip+TLV container) plus that engine's config, not a per-game Go plugin. Every engine supports full read/convert/write conversion. Unlike Unreal titles, BG3 and RE Engine titles have no fixed, config-known save filenames (slot, `.lsv`/`.bin` name), so their profiles mark images as dynamic and conversion runs need `--ps5-save-name` to say which Garlic save slot to target — see "Dynamic image resolution" in `docs/dev.md`. The `games/` metadata is embedded into both binaries at build time, so `save-sync`/`save-sync-ui` work standalone from any directory. `--games-dir` (default `games`, resolved against the current directory) points at a directory of `*.json` files that override or add to the embedded defaults by game key; the first time that directory doesn't exist, it's created and seeded with a copy of the embedded metadata so you get an editable on-disk copy without a source checkout.

The metadata maps one or more PS5 title IDs to a stable game key, and declares its engine:

```json
{
  "game": "clair",
  "name": "Clair Obscur: Expedition 33",
  "ids": [
    {
      "id": "PPSA17599",
      "region": "EU"
    }
  ],
  "engine": "unreal",
  "engine_config": {
    "module": "",
    "images": [
      { "logical": "gameplay", "save_name": "sdimg_EXPEDITION0", "pc_file": "EXPEDITION_0.sav", "payload": "ue4savegame.dpx.sav" }
    ],
    "class_equivalence": [
      { "logical": "gameplay", "pc": "BP_SaveGameObject_V8_C", "ps5": "BP_SaveGameObject_V7_C", "verified": true }
    ]
  }
}
```

`class_equivalence` rows match by class-name suffix, not full path — real Unreal Blueprint SaveGame classes are full content paths (e.g. `/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V7.BP_SaveGameObject_V7_C`), and the content-folder prefix isn't a reliable signal, so only the part after the last `.` is configured and checked. `module` only applies to native (`/Script/...`) classes; leave it empty for Blueprint-based games like Clair.

A new game on an already-supported engine (another Unreal title, for instance) is just a new `games/<key>.json` — no Go code required. See "Engine Abstraction" in `docs/dev.md` for the full schema and how to add a genuinely new engine.

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

CI runs on pushes to `master` and pull requests. Successful `master` builds upload a 1-day artifact and create a timestamp tag in `yyyy.mmdd.hhmmss` format. Releases are manual: run the Release workflow with one of those tags to create GitHub Release assets and generated release notes. The workflow also takes a `release_type` input (`prerelease` or `release`) to control whether the created GitHub Release is marked as a pre-release; it defaults to `prerelease`.

## Safety Notes

- Back up PC and PS5 saves before applying replacements.
- Save Sync PS-PC also creates `backup/<game>-<yyyymmddhhmmss>/PC` and `backup/<game>-<yyyymmddhhmmss>/PS5` before conversion.
- Disable Steam Cloud temporarily while testing converted PC files, otherwise cloud sync may overwrite local files.
- Do not point `--output-dir` at the PC save directory. The tool refuses some dangerous paths, but separate output directories are easier to inspect and recover from.
- `--apply --yes` writes replacement payloads back to PS5 through Garlic. Use the dry-run output first and verify the generated manifest.
- This project is not a PS5 save ownership, resigning, or decryption tool.
- The portability gate has been checked end-to-end against a real Clair save on both platforms (real PC save + both PS5 users) - every check passes cleanly, including the pairwise class-map/package-version checks that need both sides. That's one save family and one save state, though; if a check ever false-positives on a future save, `save-sync inspect` shows why without writing anything, and `--allow <check>` bypasses a specific one you've confirmed is fine.
- `save-sync-ui` has no authentication. Anyone who can reach its port can make this process issue GET/POST requests to whatever Garlic URL they submit. The default `--host 127.0.0.1` keeps it reachable only from the local machine; `make docker-ui` binds `0.0.0.0` so the container's published port is reachable, so only run `docker-ui` on networks you trust, and don't expose that port beyond your LAN.
