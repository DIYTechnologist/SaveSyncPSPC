# Clair Obscur: Expedition 33 Notes

This page covers the Clair-specific behavior in the Save Sync PS-PC bridge.

## Supported Target

- Game: Clair Obscur: Expedition 33
- Game key: `clair`
- PS5 title ID: `PPSA17599`
- Region: EU
- PC platform: Steam

The title ID and conversion behavior are both declared in `games/clair.json` (`"engine": "unreal"` plus its `engine_config`); there's no per-game Go package. See "Engine Abstraction" in `docs/dev.md`, and [savesync-engine's `docs/dev.md`](https://github.com/DIYTechnologist/savesync-engine/blob/main/docs/dev.md) for how the generic `engine/unreal` engine reads that profile.

## Save Images

Clair needs two Garlic save images to be treated as one logical save set:

- `sdimg_EXPEDITION0`
- `sdimg_SavesContainer`

The UI filters the full Garlic save list down to complete supported Clair groups. Other Clair save images are ignored by the bridge.

## PC Files

The PC save directory must contain:

- `EXPEDITION_0.sav`
- `SavesContainer.sav`

For `pc-to-ps5`, both files are required because the converter needs the gameplay save and the load-menu container data. For `ps5-to-pc`, the tool creates converted versions of both files in the output directory, and `--install` can back up and replace the files in the PC directory.

## Mounted Payload

Inside each Garlic-mounted PS5 image, the payload file is:

```text
ue4savegame.dpx.sav
```

The bridge downloads and uploads that payload through Garlic. It does not import a combined folder into Garlic.

## Compatibility

Current known compatible pair:

- Steam gameplay class: `BP_SaveGameObject_V8_C`
- PS5 gameplay class: `BP_SaveGameObject_V7_C`
- Compatibility: Steam V8 <-> PS5 V7 is supported

The bridge records compatibility metadata in `garlic_sync_manifest.json`. It warns if the source or target gameplay class does not match the known pair. Treat that warning seriously: newer game builds may still parse, but the data may not be semantically compatible.

## Binaries

Use the Go binaries from `bin/`, `dist/`, Docker, or GitHub Releases. Windows release assets are packaged as `.zip`; Linux release assets are packaged as `.tar.gz`.

## PS5 to PC Workflow

1. Start Garlic Save Manager on the PS5.
2. Start the UI with `make ui` or `./bin/save-sync-ui --host 127.0.0.1 --port 8765`.
3. Set the Garlic URL, for example `http://192.168.2.67:8082`.
4. Load saves and select the grouped Clair save for the correct user.
5. Run `ps5-to-pc`.
6. Inspect `backup/clair-<yyyymmddhhmmss>/PC` and `backup/clair-<yyyymmddhhmmss>/PS5`.
7. Inspect the generated output directory and manifest.
8. Use install only when ready to replace the local PC files.

Equivalent CLI shape:

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

Add `--install` to back up and replace the PC files in `--pc-dir`.

## PC to PS5 Workflow

1. Confirm the PC directory contains `EXPEDITION_0.sav` and `SavesContainer.sav`.
2. Confirm Garlic shows the matching PS5 save images for the target PS5 user.
3. Run `pc-to-ps5` without apply first.
4. Inspect `backup/clair-<yyyymmddhhmmss>/PC` and `backup/clair-<yyyymmddhhmmss>/PS5`.
5. Inspect `garlic_sync_manifest.json` and any compatibility warnings.
6. Re-run with `--apply --yes` only when ready to replace PS5 payloads.
7. Start Clair on PS5 and verify that the load menu shows the expected save.

Equivalent CLI dry run:

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

Apply through Garlic:

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

## Important Notes

- Keep backups of both PC and PS5 saves.
- Each conversion run creates a backup directory with unmodified `PC/` and `PS5/` copies before conversion or upload.
- Disable Steam Cloud while testing converted PC files.
- The converter uses a known-compatible envelope graft. It is not a full property-aware save editor.
- The bridge relies on Garlic to handle PS5 mount, decrypt, upload, and re-encrypt behavior.
- Do not use Garlic Import with a combined directory for this flow. Use existing installed PS5 save images and replace the root payloads through Garlic.
- Backup images shown by Garlic are intentionally filtered out.

## Troubleshooting

`Missing PC save file(s)` means the selected PC directory does not contain both required files. For Clair, `EXPEDITION_0.sav` and `SavesContainer.sav` must be present before running `pc-to-ps5`.

If the UI shows raw Garlic saves but no supported Clair group, confirm that both `sdimg_EXPEDITION0` and `sdimg_SavesContainer` are present for the same `PPSA17599` user and are not backup or USB entries.

If the converted save does not appear correctly in-game, restore backups and check the manifest compatibility warnings before trying another conversion.
