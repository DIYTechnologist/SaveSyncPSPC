# Roadmap

## Feature 1: Cloud/NAS Save Sync

Goal: sync converted and raw backup saves to external storage, then detect new or updated saves from PlayStation or PC automatically.

Target destinations:

- Google Drive
- NAS shares
- Local network folders
- Other cloud-backed directories

Core behavior:

- Watch PC save directories for new or changed files.
- Watch Save Sync PS-PC backup/output directories for new conversion artifacts.
- Poll Garlic/PlayStation saves for new or updated save payloads.
- Copy or sync saves into a configured destination layout.
- Preserve the existing backup layout:

```text
backup/<game>-<yyyymmddhhmmss>/
  PC/
  PS5/
```

Expected config shape:

```json
{
  "sync": {
    "enabled": true,
    "destinations": [
      {
        "type": "directory",
        "path": "/mnt/nas/saves"
      },
      {
        "type": "google-drive",
        "remote": "Save Sync PS-PC"
      }
    ],
    "watch": {
      "pc_dirs": true,
      "backup_dirs": true,
      "garlic_poll_seconds": 300
    }
  }
}
```

Open decisions:

- Use direct Google Drive API support or rely on mounted/rclone-backed folders.
- Whether NAS support should be plain filesystem paths only, or include SMB/NFS helpers.
- Conflict policy when PC and PS saves both change before the next sync.
- Retention policy for old backups.
- Whether watch mode belongs in `save-sync-ui`, a new `save-sync watch` command, or both.

Initial implementation path:

1. Add config loading.
2. Add destination abstraction for local/mounted directories.
3. Add filesystem watcher for PC and backup/output folders.
4. Add Garlic polling for changed PS save metadata.
5. Add conflict-safe copy with manifest records.
6. Add Google Drive/rclone/NAS-specific destination support.
