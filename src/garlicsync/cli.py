#!/usr/bin/env python3
"""Save Sync PS-PC command-line bridge.

Games are discovered from games/*.json. A metadata file maps PS5 title IDs to a
game key, and target-platform plugins live beside it as:

    games/<game>-pc.py
    games/<game>-ps5.py

Garlic currently owns PS5 decrypt/mount/encrypt. This tool moves payload files
over Garlic's HTTP API and runs the selected game's conversion plugin locally.
"""

from __future__ import annotations

import argparse
import datetime as dt
import importlib.util
import json
import os
import shutil
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from types import ModuleType
from typing import Any

TOOL_VERSION = "0.2.0"
DEFAULT_GAMES_DIR = Path(__file__).resolve().parent / "games"


class BridgeError(RuntimeError):
    pass


@dataclass(frozen=True)
class GameProfile:
    key: str
    name: str
    title_ids: tuple[str, ...]
    metadata_path: Path


def atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())
        tmp = Path(handle.name)
    tmp.replace(path)


def load_module(path: Path, module_name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(module_name, path)
    if spec is None or spec.loader is None:
        raise BridgeError(f"Cannot load plugin: {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module


def load_game_profiles(games_dir: Path) -> dict[str, GameProfile]:
    profiles: dict[str, GameProfile] = {}
    if not games_dir.is_dir():
        raise BridgeError(f"Games directory not found: {games_dir}")

    for path in sorted(games_dir.glob("*.json")):
        try:
            raw = json.loads(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            raise BridgeError(f"Invalid game metadata JSON: {path}: {exc}") from exc

        key = str(raw.get("game") or path.stem).strip()
        if not key:
            raise BridgeError(f"Missing game key in {path}")

        ids_raw = raw.get("ids", raw.get("id"))
        if isinstance(ids_raw, str):
            title_ids = (ids_raw.upper(),)
        elif isinstance(ids_raw, list):
            parsed: list[str] = []
            for item in ids_raw:
                if isinstance(item, str):
                    parsed.append(item.upper())
                elif isinstance(item, dict) and item.get("id"):
                    parsed.append(str(item["id"]).upper())
            title_ids = tuple(parsed)
        else:
            title_ids = ()

        if not title_ids:
            raise BridgeError(f"No PS5 title ids defined in {path}")
        profiles[key] = GameProfile(
            key=key,
            name=str(raw.get("name") or key),
            title_ids=title_ids,
            metadata_path=path,
        )
    return profiles


def plugin_for(games_dir: Path, game: str, target: str) -> ModuleType:
    path = games_dir / f"{game}-{target}.py"
    if not path.is_file():
        raise BridgeError(f"Missing {target} plugin for game '{game}': {path}")
    return load_module(path, f"garlic_game_{game}_{target}")


class GarlicClient:
    def __init__(self, base_url: str, timeout: float = 120.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def _url(self, path: str, query: dict[str, object] | None = None) -> str:
        url = self.base_url + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        return url

    def request_bytes(
        self,
        path: str,
        query: dict[str, object] | None = None,
        data: bytes | None = None,
        content_type: str = "application/octet-stream",
    ) -> tuple[bytes, str]:
        headers = {}
        if data is not None:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(self._url(path, query), data=data, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                return resp.read(), resp.headers.get("Content-Type", "")
        except urllib.error.URLError as exc:
            raise BridgeError(f"Garlic request failed: {exc}") from exc

    def request_json(
        self,
        path: str,
        query: dict[str, object] | None = None,
        data: bytes | None = None,
    ) -> dict[str, Any]:
        body, _ctype = self.request_bytes(path, query=query, data=data)
        try:
            value = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise BridgeError(f"Garlic returned non-JSON response from {path}") from exc
        if not isinstance(value, dict):
            raise BridgeError(f"Garlic returned unexpected JSON from {path}")
        if value.get("error"):
            raise BridgeError(str(value["error"]))
        return value

    def saves(self) -> list[dict[str, Any]]:
        data = self.request_json("/api/saves")
        saves = data.get("saves")
        if not isinstance(saves, list):
            raise BridgeError("Garlic /api/saves response did not contain a saves list")
        return [s for s in saves if isinstance(s, dict)]

    def users(self) -> list[dict[str, Any]]:
        data = self.request_json("/api/users")
        users = data.get("users")
        if not isinstance(users, list):
            raise BridgeError("Garlic /api/users response did not contain a users list")
        return [u for u in users if isinstance(u, dict)]

    def mount(self, idx: int) -> dict[str, Any]:
        return self.request_json("/api/mount", {"idx": idx})

    def unmount(self) -> None:
        self.request_json("/api/unmount")

    def download_file(self, name: str) -> bytes:
        body, ctype = self.request_bytes("/api/download_file", {"name": name})
        stripped = body[:64].lstrip()
        if "application/json" in ctype or stripped.startswith(b"{"):
            try:
                value = json.loads(body.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                value = {}
            raise BridgeError(str(value.get("error") or "Garlic download failed"))
        return body

    def upload_file(self, name: str, data: bytes) -> None:
        self.request_json("/api/upload_file", {"name": name}, data=data)

    def find_save_index(
        self,
        title_ids: tuple[str, ...],
        save_name: str,
        uid: str | None = None,
    ) -> int:
        title_set = {title_id.upper() for title_id in title_ids}
        matches: list[tuple[int, dict[str, Any]]] = []
        for idx, save in enumerate(self.saves()):
            if str(save.get("title_id", "")).upper() not in title_set:
                continue
            if save.get("save_name") != save_name:
                continue
            if save.get("type") != "ps5" or save.get("backup") or save.get("usb"):
                continue
            if uid and save.get("uid") != uid:
                continue
            matches.append((idx, save))

        if not matches:
            suffix = f" for uid {uid}" if uid else ""
            ids = ", ".join(title_ids)
            raise BridgeError(f"Could not find {ids}/{save_name}{suffix} in Garlic")
        if len(matches) > 1:
            choices = ", ".join(f"idx={idx} uid={save.get('uid', '')}" for idx, save in matches)
            raise BridgeError(f"Multiple {save_name} saves matched; pass --ps5-uid ({choices})")
        return matches[0][0]

    def fetch_payload(
        self,
        title_ids: tuple[str, ...],
        save_name: str,
        payload_name: str,
        uid: str | None = None,
    ) -> bytes:
        idx = self.find_save_index(title_ids, save_name, uid)
        self.mount(idx)
        try:
            return self.download_file(payload_name)
        finally:
            self.unmount()

    def replace_payload(
        self,
        title_ids: tuple[str, ...],
        save_name: str,
        payload_name: str,
        data: bytes,
        uid: str | None = None,
    ) -> None:
        idx = self.find_save_index(title_ids, save_name, uid)
        self.mount(idx)
        try:
            self.upload_file(payload_name, data)
        finally:
            self.unmount()


def refuse_dangerous_output(output_dir: Path, protected: list[Path]) -> None:
    resolved = output_dir.resolve()
    dangerous = {Path.cwd().resolve(), Path.home().resolve()}
    dangerous.update(path.resolve() for path in protected)
    if resolved in dangerous:
        raise BridgeError(f"Refusing to use dangerous output directory: {resolved}")


def prepare_output_dir(output_dir: Path, force: bool, protected: list[Path]) -> None:
    refuse_dangerous_output(output_dir, protected)
    if output_dir.exists():
        if not force:
            raise BridgeError(f"Output directory already exists: {output_dir}; use --force")
        shutil.rmtree(output_dir)
    output_dir.mkdir(parents=True)


def print_plugin_warnings(plugin_manifest: dict[str, object]) -> None:
    warnings = plugin_manifest.get("warnings", [])
    if not warnings:
        return
    print("Compatibility warnings:")
    for warning in warnings:
        print(f"  - {warning}")


def select_profile(args: argparse.Namespace, garlic: GarlicClient | None = None) -> GameProfile:
    profiles = load_game_profiles(args.games_dir)
    if args.game:
        profile = profiles.get(args.game)
        if profile is None:
            raise BridgeError(f"Unknown game '{args.game}'. Available: {', '.join(sorted(profiles))}")
        return profile

    if args.title_id:
        wanted = args.title_id.upper()
        for profile in profiles.values():
            if wanted in profile.title_ids:
                return profile
        raise BridgeError(f"No game metadata maps title id {wanted}")

    if garlic is None:
        raise BridgeError("Internal error: Garlic client is required for auto-discovery")

    seen_ids = {str(save.get("title_id", "")).upper() for save in garlic.saves()}
    matches = [profile for profile in profiles.values() if seen_ids.intersection(profile.title_ids)]
    if not matches:
        raise BridgeError("Could not auto-discover a supported game from Garlic saves")
    if len(matches) > 1:
        names = ", ".join(f"{p.key} ({p.name})" for p in matches)
        raise BridgeError(f"Multiple supported games found; pass --game. Matches: {names}")
    return matches[0]


def ps5_to_pc(args: argparse.Namespace) -> int:
    pc_dir = args.pc_dir.resolve()
    output_dir = args.output_dir.resolve()
    garlic = GarlicClient(args.garlic, args.timeout)
    profile = select_profile(args, garlic)
    ps5_plugin = plugin_for(args.games_dir, profile.key, "ps5")
    pc_plugin = plugin_for(args.games_dir, profile.key, "pc")

    prepare_output_dir(output_dir, args.force, [pc_dir])

    payload_name = ps5_plugin.PAYLOAD_NAME
    ps5_payloads: dict[str, bytes] = {}
    for image in ps5_plugin.SAVE_IMAGES:
        logical = image["logical"]
        save_name = image["save_name"]
        print(f"Pulling {profile.name} {save_name}/{payload_name} from Garlic...")
        ps5_payloads[logical] = garlic.fetch_payload(
            profile.title_ids,
            save_name,
            payload_name,
            args.ps5_uid,
        )

    outputs, plugin_manifest = pc_plugin.convert_from_ps5(ps5_payloads, pc_dir)
    print_plugin_warnings(plugin_manifest)
    for relpath, data in outputs.items():
        atomic_write(output_dir / relpath, data)

    manifest = {
        "tool_version": TOOL_VERSION,
        "created": dt.datetime.now(dt.UTC).isoformat(),
        "direction": "ps5-to-pc-via-garlic",
        "game": profile.key,
        "game_name": profile.name,
        "title_ids": profile.title_ids,
        "garlic": args.garlic,
        "ps5_uid": args.ps5_uid,
        "plugin": plugin_manifest,
    }
    atomic_write(output_dir / "garlic_sync_manifest.json", json.dumps(manifest, indent=2).encode("utf-8"))

    print(f"Created converted PC files in: {output_dir}")
    if args.install:
        backup_dir = pc_plugin.install_outputs(outputs, pc_dir)
        print(f"Installed into PC directory: {pc_dir}")
        print(f"Previous PC files backed up to: {backup_dir}")
    return 0


def pc_to_ps5(args: argparse.Namespace) -> int:
    pc_dir = args.pc_dir.resolve()
    output_dir = args.output_dir.resolve()
    garlic = GarlicClient(args.garlic, args.timeout)
    profile = select_profile(args, garlic)
    ps5_plugin = plugin_for(args.games_dir, profile.key, "ps5")

    prepare_output_dir(output_dir, args.force, [pc_dir])

    payload_name = ps5_plugin.PAYLOAD_NAME
    ps5_templates: dict[str, bytes] = {}
    for image in ps5_plugin.SAVE_IMAGES:
        logical = image["logical"]
        save_name = image["save_name"]
        print(f"Pulling PS5 template {profile.name} {save_name}/{payload_name} from Garlic...")
        ps5_templates[logical] = garlic.fetch_payload(
            profile.title_ids,
            save_name,
            payload_name,
            args.ps5_uid,
        )

    replacements, plugin_manifest = ps5_plugin.convert_to_ps5(pc_dir, ps5_templates)
    print_plugin_warnings(plugin_manifest)
    for save_name, data in replacements.items():
        atomic_write(output_dir / save_name / payload_name, data)

    manifest = {
        "tool_version": TOOL_VERSION,
        "created": dt.datetime.now(dt.UTC).isoformat(),
        "direction": "pc-to-ps5-via-garlic",
        "game": profile.key,
        "game_name": profile.name,
        "title_ids": profile.title_ids,
        "garlic": args.garlic,
        "ps5_uid": args.ps5_uid,
        "applied_to_ps5": bool(args.apply),
        "plugin": plugin_manifest,
    }
    atomic_write(output_dir / "garlic_sync_manifest.json", json.dumps(manifest, indent=2).encode("utf-8"))
    print(f"Created PS5 replacement payloads in: {output_dir}")

    if args.apply:
        if not args.yes:
            raise BridgeError("Refusing to write to PS5 without --yes")
        for save_name, data in replacements.items():
            print(f"Replacing {profile.name} {save_name}/{payload_name} through Garlic...")
            garlic.replace_payload(profile.title_ids, save_name, payload_name, data, args.ps5_uid)
        print("Applied converted payloads to PS5. Start the game and verify the load menu.")
    else:
        print("Dry run only. Re-run with --apply --yes to replace PS5 payloads.")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Save Sync PS-PC: sync supported games between PlayStation save managers and PC directories."
    )
    parser.add_argument("--garlic", required=True, help="Garlic base URL, e.g. http://192.168.1.50:8082")
    parser.add_argument("--timeout", type=float, default=120.0, help="HTTP timeout in seconds")
    parser.add_argument("--ps5-uid", help="Optional PS5 user id filter from Garlic /api/saves")
    parser.add_argument("--games-dir", type=Path, default=DEFAULT_GAMES_DIR, help="Game plugin directory")
    parser.add_argument("--game", help="Game key from games/<game>.json, e.g. clair")
    parser.add_argument("--title-id", help="PS5 title id to map through games/*.json, e.g. PPSA17599")
    sub = parser.add_subparsers(dest="command", required=True)

    p2c = sub.add_parser(
        "ps5-to-pc",
        aliases=["ps5-to-steam"],
        help="Pull PS5 saves through Garlic and create PC files",
    )
    p2c.add_argument("--pc-dir", "--steam-dir", dest="pc_dir", type=Path, required=True, help="PC save directory")
    p2c.add_argument("--output-dir", type=Path, default=Path("garlic_ps5_to_pc"))
    p2c.add_argument("--install", action="store_true", help="Back up and replace files in --pc-dir")
    p2c.add_argument("--force", action="store_true", help="Replace existing output directory")
    p2c.set_defaults(func=ps5_to_pc)

    c2p = sub.add_parser(
        "pc-to-ps5",
        aliases=["steam-to-ps5"],
        help="Convert local PC saves for PS5 and optionally apply through Garlic",
    )
    c2p.add_argument("--pc-dir", "--steam-dir", dest="pc_dir", type=Path, required=True, help="PC save directory")
    c2p.add_argument("--output-dir", type=Path, default=Path("garlic_pc_to_ps5"))
    c2p.add_argument("--apply", action="store_true", help="Replace PS5 payloads through Garlic")
    c2p.add_argument("--yes", action="store_true", help="Confirm --apply writes to PS5")
    c2p.add_argument("--force", action="store_true", help="Replace existing output directory")
    c2p.set_defaults(func=pc_to_ps5)
    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    try:
        return args.func(args)
    except (BridgeError, OSError, ValueError, KeyError, AttributeError) as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
