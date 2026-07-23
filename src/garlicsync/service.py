#!/usr/bin/env python3
"""Local browser UI for Save Sync PS-PC."""

from __future__ import annotations

import argparse
import contextlib
import io
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace
from typing import Any

from garlicsync.cli import (
    DEFAULT_GAMES_DIR,
    BridgeError,
    GarlicClient,
    load_game_profiles,
    pc_to_ps5,
    plugin_for,
    ps5_to_pc,
)

INDEX_HTML = r"""<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Save Sync PS-PC</title>
  <style>
    :root {
      --bg:#101114; --panel:#181a20; --panel2:#20232b; --bd:#343844;
      --tx:#e9edf5; --mut:#9ba4b5; --ac:#4da3ff; --ok:#37d67a; --er:#ff5c6c;
    }
    * { box-sizing:border-box }
    body {
      margin:0; background:var(--bg); color:var(--tx);
      font-family:system-ui,-apple-system,Segoe UI,sans-serif;
    }
    header {
      height:56px; display:flex; align-items:center; gap:14px;
      padding:0 18px; background:#15171d; border-bottom:1px solid var(--bd);
    }
    h1 { font-size:18px; margin:0; font-weight:700 }
    main {
      display:grid; grid-template-columns:360px 1fr; gap:16px;
      padding:16px; min-height:calc(100vh - 56px);
    }
    section {
      background:var(--panel); border:1px solid var(--bd);
      border-radius:8px; padding:14px;
    }
    label { display:block; color:var(--mut); font-size:12px; margin:12px 0 5px }
    input, select {
      width:100%; background:var(--panel2); color:var(--tx);
      border:1px solid var(--bd); border-radius:6px; padding:9px 10px;
      font:inherit; font-size:14px;
    }
    .row { display:grid; grid-template-columns:1fr 1fr; gap:10px }
    button {
      background:var(--ac); color:#07111f; border:0; border-radius:6px;
      padding:9px 12px; font-weight:700; cursor:pointer; margin-top:12px;
    }
    button.secondary { background:var(--panel2); color:var(--tx); border:1px solid var(--bd) }
    button.danger { background:var(--er); color:#fff }
    button:disabled { opacity:.45; cursor:not-allowed }
    .actions { display:flex; gap:8px; flex-wrap:wrap }
    .save {
      display:grid; grid-template-columns:120px 1fr 120px 110px; gap:8px;
      border-bottom:1px solid var(--bd); padding:9px 0; align-items:center;
      font-size:13px; cursor:pointer;
    }
    .save:first-child { border-top:1px solid var(--bd) }
    .save:hover { background:#1d2129 }
    .save.selected { background:#1b3047; outline:1px solid var(--ac) }
    .filters { display:grid; grid-template-columns:1fr 1fr 1fr; gap:10px; margin-bottom:10px }
    .checkrow { display:flex; align-items:center; gap:8px; color:var(--mut); font-size:13px; margin:10px 0 }
    .checkrow input { width:auto }
    .mut { color:var(--mut) }
    .pill {
      display:inline-block; padding:2px 7px; border-radius:999px;
      background:#283142; color:#bcd7ff; font-size:11px; font-weight:700;
    }
    pre {
      background:#0a0b0e; border:1px solid var(--bd); border-radius:8px;
      padding:12px; overflow:auto; white-space:pre-wrap; min-height:220px;
      color:#c8d2e2;
    }
    .ok { color:var(--ok) }
    .err { color:var(--er) }
    @media (max-width: 860px) {
      main { grid-template-columns:1fr }
      .save { grid-template-columns:1fr }
    }
  </style>
</head>
<body>
  <header>
    <h1>Save Sync PS-PC</h1>
    <span class="mut">PC bridge for PlayStation saves</span>
  </header>
  <main>
    <section>
      <label>Garlic URL</label>
      <input id="garlic" value="http://192.168.2.67:8082">

      <div class="row">
        <div>
          <label>Game</label>
          <select id="game" onchange="updateCompatibility()"></select>
          <div id="compat" class="mut" style="font-size:12px;margin-top:6px"></div>
        </div>
        <div>
          <label>PS5 User</label>
          <select id="uid"><option value="">Auto / all users</option></select>
        </div>
      </div>

      <label>PC save directory</label>
      <input id="pcdir" placeholder="/path/to/SaveGames or C:\Users\...\SaveGames">

      <label>Output directory</label>
      <input id="outdir" value="./garlic_ui_output">

      <div class="row">
        <div>
          <label>Direction</label>
          <select id="direction">
            <option value="ps5-to-pc">PS5 to PC</option>
            <option value="pc-to-ps5">PC to PS5</option>
          </select>
        </div>
        <div>
          <label>Timeout seconds</label>
          <input id="timeout" value="120">
        </div>
      </div>

      <div class="actions">
        <button onclick="loadSaves()">Get Saves</button>
        <button onclick="runSync(false)">Validate / Convert</button>
        <button class="danger" onclick="runSync(true)">Apply to PS5</button>
      </div>
      <div class="actions">
        <button class="secondary" onclick="runInstall()">Install to PC Dir</button>
      </div>
      <p class="mut">Apply writes converted payloads back through Garlic. Install backs up and replaces files in the PC directory.</p>
    </section>

    <section>
      <h2 style="font-size:15px;margin:0 0 10px">Garlic Saves</h2>
      <div class="filters">
        <div>
          <label style="margin-top:0">Filter user</label>
          <select id="filterUser" onchange="renderSaves()"><option value="">All users</option></select>
        </div>
        <div>
          <label style="margin-top:0">Filter game</label>
          <select id="filterGame" onchange="renderSaves()"><option value="">All games</option></select>
        </div>
        <div>
          <label style="margin-top:0">Search</label>
          <input id="filterText" oninput="renderSaves()" placeholder="title, save, uid">
        </div>
      </div>
      <label class="checkrow"><input type="checkbox" id="showRaw" onchange="renderSaves()"> Show raw Garlic saves</label>
      <div id="saves" class="mut">Click Get Saves.</div>
      <h2 style="font-size:15px;margin:18px 0 10px">Log</h2>
      <pre id="log"></pre>
    </section>
  </main>
<script>
const $ = id => document.getElementById(id);
let allSaves = [];
let allGroups = [];
let allUsers = [];
let allGames = [];
let selectedGroupKey = '';

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({
    '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;'
  }[c]));
}
function log(text) {
  const el = $('log');
  el.textContent += text + '\n';
  el.scrollTop = el.scrollHeight;
}
async function api(path, body) {
  const res = await fetch(path, {
    method: body ? 'POST' : 'GET',
    headers: body ? {'Content-Type':'application/json'} : {},
    body: body ? JSON.stringify(body) : undefined
  });
  const data = await res.json();
  if (!res.ok || data.error) throw new Error(data.error || res.statusText);
  return data;
}
async function loadGames() {
  try {
    const data = await api('/api/games');
    allGames = data.games || [];
    const options = allGames.map(g =>
      `<option value="${esc(g.key)}">${esc(g.name)} (${esc(g.ids.join(', '))})</option>`
    ).join('');
    $('game').innerHTML = options;
    $('filterGame').innerHTML = '<option value="">All games</option>' + options;
    updateCompatibility();
  } catch (e) { log('Games error: ' + e.message); }
}

function updateCompatibility() {
  const game = allGames.find(g => g.key === $('game').value);
  if (!game || !game.compatibility) {
    $('compat').textContent = '';
    return;
  }
  const c = game.compatibility;
  $('compat').textContent = `${c.pc.platform} ${c.pc.version} ↔ ${c.ps5.platform} ${c.ps5.version}: ${c.convertible ? 'convertible' : 'not marked convertible'}`;
}

function userLabel(uid) {
  const user = allUsers.find(u => u.id === uid);
  return user ? `${user.name} (${user.id})` : (uid || 'unknown');
}

function populateUsers(users, saves) {
  const byId = new Map();
  for (const user of users || []) {
    if (user.id) byId.set(user.id, user.name || user.id);
  }
  for (const save of saves || []) {
    if (save.uid && !byId.has(save.uid)) byId.set(save.uid, save.uid);
  }
  allUsers = Array.from(byId, ([id, name]) => ({id, name}));
  const opts = allUsers.map(u => `<option value="${esc(u.id)}">${esc(u.name)} (${esc(u.id)})</option>`).join('');
  $('uid').innerHTML = '<option value="">Auto / all users</option>' + opts;
  $('filterUser').innerHTML = '<option value="">All users</option>' + opts;
}

async function loadSaves() {
  $('saves').textContent = 'Loading...';
  try {
    const data = await api('/api/saves', {
      garlic: $('garlic').value,
      timeout: Number($('timeout').value || 120)
    });
    allSaves = data.saves || [];
    allGroups = data.groups || [];
    populateUsers(data.users || [], allSaves);
    renderSaves();
    log('Loaded ' + allSaves.length + ' raw saves, ' + allGroups.length + ' supported groups and ' + allUsers.length + ' users from Garlic.');
  } catch (e) {
    $('saves').innerHTML = `<span class="err">${e.message}</span>`;
    log('Saves error: ' + e.message);
  }
}

function selectGroup(i) {
  const group = allGroups[i];
  if (!group) return;
  selectedGroupKey = group.key;
  if (group.uid) {
    $('uid').value = group.uid;
    $('filterUser').value = group.uid;
  }
  if (group.game) {
    $('game').value = group.game;
    $('filterGame').value = group.game;
    updateCompatibility();
  }
  renderSaves();
  log('Selected ' + group.game_name + ' / ' + group.title_id + ' / ' + userLabel(group.uid));
}

function renderSaves() {
  if ($('showRaw').checked) {
    renderRawSaves();
    return;
  }
  const user = $('filterUser').value;
  const game = $('filterGame').value;
  const q = $('filterText').value.trim().toLowerCase();
  const rows = allGroups
    .map((g, i) => ({g, i}))
    .filter(({g}) => !user || g.uid === user)
    .filter(({g}) => !game || g.game === game)
    .filter(({g}) => {
      if (!q) return true;
      return [g.title_id, g.title_name, g.uid, g.game, g.game_name, g.images.map(x => x.save_name).join(' ')]
        .some(v => String(v || '').toLowerCase().includes(q));
    });

  if (!rows.length) {
    $('saves').innerHTML = '<span class="mut">No complete supported save groups match the current filters.</span>';
    return;
  }
  $('saves').innerHTML = rows.map(({g, i}) => {
    const selected = g.key === selectedGroupKey ? ' selected' : '';
    const imageText = g.images.map(x => x.save_name).join(' + ');
    return `<div class="save${selected}" onclick="selectGroup(${i})">
      <div><span class="pill">${esc(g.game)}</span><br><span class="mut">${esc(userLabel(g.uid))}</span></div>
      <div><b>${esc(g.game_name)}</b><br><span class="mut">${esc(g.title_id)} / ${esc(imageText)}</span></div>
      <div class="ok">complete</div>
      <div class="mut">${g.images.length} images</div>
    </div>`;
  }).join('');
}

function renderRawSaves() {
  const user = $('filterUser').value;
  const game = $('filterGame').value;
  const q = $('filterText').value.trim().toLowerCase();
  const rows = allSaves
    .map(s => ({s}))
    .filter(({s}) => !user || s.uid === user)
    .filter(({s}) => !game || s.supported_game === game)
    .filter(({s}) => {
      if (!q) return true;
      return [s.title_id, s.title_name, s.save_name, s.uid, s.path, s.supported_game]
        .some(v => String(v || '').toLowerCase().includes(q));
    });

  if (!rows.length) {
    $('saves').innerHTML = '<span class="mut">No raw saves match the current filters.</span>';
    return;
  }
  $('saves').innerHTML = rows.map(({s}) => {
    const flags = [s.backup ? 'backup' : '', s.usb ? 'usb' : ''].filter(Boolean).join(' ');
    return `<div class="save">
      <div><span class="pill">${esc(s.type || '')}</span><br><span class="mut">${esc(userLabel(s.uid))}</span></div>
      <div><b>${esc(s.title_name || s.title_id)}</b><br><span class="mut">${esc(s.title_id)} / ${esc(s.save_name)}</span></div>
      <div>${s.supported_game ? esc(s.supported_game) : '<span class="mut">unsupported</span>'}</div>
      <div class="mut">${esc(flags)}</div>
    </div>`;
  }).join('');
}

function requestBase() {
  return {
    garlic: $('garlic').value,
    game: $('game').value,
    ps5_uid: $('uid').value || null,
    pc_dir: $('pcdir').value.trim(),
    output_dir: $('outdir').value,
    timeout: Number($('timeout').value || 120),
    force: true
  };
}
async function runSync(apply) {
  const body = requestBase();
  body.direction = $('direction').value;
  body.apply = apply;
  body.yes = apply;
  body.install = false;
  try {
    log('Running ' + body.direction + (apply ? ' with PS5 apply...' : '...'));
    const data = await api('/api/run', body);
    log(data.log || 'Done.');
  } catch (e) { log('Run error: ' + e.message); }
}
async function runInstall() {
  const body = requestBase();
  body.direction = 'ps5-to-pc';
  body.install = true;
  body.apply = false;
  body.yes = false;
  try {
    log('Running PS5 to PC with install...');
    const data = await api('/api/run', body);
    log(data.log || 'Done.');
  } catch (e) { log('Install error: ' + e.message); }
}
loadGames();
</script>
</body>
</html>
"""


class Handler(BaseHTTPRequestHandler):
    games_dir = DEFAULT_GAMES_DIR

    def log_message(self, fmt: str, *args: object) -> None:
        print("[ui]", fmt % args)

    def send_json(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0:
            return {}
        raw = self.rfile.read(length)
        value = json.loads(raw.decode("utf-8"))
        if not isinstance(value, dict):
            raise ValueError("Expected JSON object")
        return value

    def supported_groups(self, saves: list[dict[str, Any]]) -> list[dict[str, Any]]:
        profiles = load_game_profiles(self.games_dir)
        groups: list[dict[str, Any]] = []
        for profile in profiles.values():
            ps5_plugin = plugin_for(self.games_dir, profile.key, "ps5")
            required_images = list(ps5_plugin.SAVE_IMAGES)
            required_names = [str(image["save_name"]) for image in required_images]
            title_set = {title_id.upper() for title_id in profile.title_ids}
            buckets: dict[tuple[str, str], dict[str, dict[str, Any]]] = {}
            for save in saves:
                if str(save.get("title_id", "")).upper() not in title_set:
                    continue
                if save.get("type") != "ps5" or save.get("backup") or save.get("usb"):
                    continue
                save_name = str(save.get("save_name", ""))
                if save_name not in required_names:
                    continue
                key = (str(save.get("uid", "")), str(save.get("title_id", "")).upper())
                buckets.setdefault(key, {})[save_name] = save

            for (uid, title_id), by_name in buckets.items():
                present = [name for name in required_names if name in by_name]
                missing = [name for name in required_names if name not in by_name]
                if missing:
                    continue
                first = by_name[present[0]]
                groups.append(
                    {
                        "key": f"{profile.key}|{uid}|{title_id}",
                        "game": profile.key,
                        "game_name": profile.name,
                        "title_id": title_id,
                        "title_name": first.get("title_name") or profile.name,
                        "uid": uid,
                        "complete": True,
                        "images": [
                            {
                                "logical": image["logical"],
                                "label": image["label"],
                                "save_name": image["save_name"],
                                "idx": by_name[image["save_name"]].get("idx"),
                            }
                            for image in required_images
                        ],
                    }
                )
        groups.sort(key=lambda g: (str(g["game_name"]), str(g["uid"]), str(g["title_id"])))
        return groups

    def validate_group_only(self, req: dict[str, Any]) -> str:
        garlic = GarlicClient(str(req.get("garlic", "")).strip(), float(req.get("timeout", 120)))
        saves = garlic.saves()
        groups = self.supported_groups(saves)
        game = str(req.get("game") or "")
        uid = str(req.get("ps5_uid") or "")
        if game:
            groups = [group for group in groups if group.get("game") == game]
        if uid:
            groups = [group for group in groups if group.get("uid") == uid]
        if not groups:
            raise BridgeError("No complete supported save group matched the selected game/user.")
        if len(groups) > 1:
            choices = "\n".join(
                f"  - {group['game_name']} / {group['title_id']} / uid {group['uid']}" for group in groups
            )
            raise BridgeError(f"Multiple groups matched. Select a PS5 user first:\n{choices}")

        group = groups[0]
        images = "\n".join(f"  - {image['save_name']} ({image['label']})" for image in group["images"])
        return (
            "Dry run validation OK.\n"
            f"Selected: {group['game_name']} / {group['title_id']} / uid {group['uid']}\n"
            "Required Garlic save images are present:\n"
            f"{images}\n\n"
            "No PC save directory was supplied, so no conversion was attempted.\n"
            "For PS5 -> PC conversion, provide a PC/Steam save directory containing "
            "EXPEDITION_0.sav and SavesContainer.sav so the converter has destination envelopes."
        )

    def do_GET(self) -> None:
        try:
            if self.path == "/" or self.path == "/index.html":
                body = INDEX_HTML.encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            if self.path == "/api/games":
                profiles = load_game_profiles(self.games_dir)
                games = []
                for profile in profiles.values():
                    compatibility = None
                    try:
                        compatibility = plugin_for(self.games_dir, profile.key, "ps5").COMPATIBILITY
                    except Exception:
                        compatibility = None
                    games.append(
                        {
                            "key": profile.key,
                            "name": profile.name,
                            "ids": list(profile.title_ids),
                            "compatibility": compatibility,
                        }
                    )
                self.send_json(200, {"games": games})
                return
            self.send_json(404, {"error": "not found"})
        except Exception as exc:
            self.send_json(500, {"error": str(exc)})

    def do_POST(self) -> None:
        try:
            if self.path == "/api/saves":
                req = self.read_json()
                garlic = GarlicClient(str(req.get("garlic", "")).strip(), float(req.get("timeout", 120)))
                users: list[dict[str, Any]] = []
                try:
                    users = garlic.users()
                except Exception as exc:
                    users = [{"id": "", "name": f"Unable to load users: {exc}"}]
                saves = garlic.saves()
                profiles = load_game_profiles(self.games_dir)
                by_id = {
                    title_id: profile.key
                    for profile in profiles.values()
                    for title_id in profile.title_ids
                }
                for save in saves:
                    save["supported_game"] = by_id.get(str(save.get("title_id", "")).upper())
                user_names = {
                    str(user.get("id")): str(user.get("name", user.get("id", "")))
                    for user in users
                    if user.get("id")
                }
                for idx, save in enumerate(saves):
                    save["idx"] = idx
                    save["user_name"] = user_names.get(str(save.get("uid", "")), "")
                self.send_json(200, {"saves": saves, "groups": self.supported_groups(saves), "users": users})
                return

            if self.path == "/api/users":
                req = self.read_json()
                garlic = GarlicClient(str(req.get("garlic", "")).strip(), float(req.get("timeout", 120)))
                self.send_json(200, {"users": garlic.users()})
                return

            if self.path == "/api/run":
                req = self.read_json()
                direction = str(req.get("direction", ""))
                pc_dir_raw = str(req.get("pc_dir") or "").strip()
                is_validate_only = not pc_dir_raw and not req.get("install") and not req.get("apply")
                if is_validate_only:
                    self.send_json(200, {"ok": True, "log": self.validate_group_only(req)})
                    return
                args = SimpleNamespace(
                    garlic=str(req["garlic"]),
                    timeout=float(req.get("timeout", 120)),
                    ps5_uid=req.get("ps5_uid") or None,
                    games_dir=self.games_dir,
                    game=req.get("game") or None,
                    title_id=req.get("title_id") or None,
                    pc_dir=Path(pc_dir_raw),
                    output_dir=Path(str(req["output_dir"])),
                    force=bool(req.get("force", True)),
                    install=bool(req.get("install", False)),
                    apply=bool(req.get("apply", False)),
                    yes=bool(req.get("yes", False)),
                )
                buf = io.StringIO()
                with contextlib.redirect_stdout(buf):
                    if direction == "ps5-to-pc":
                        ps5_to_pc(args)
                    elif direction == "pc-to-ps5":
                        pc_to_ps5(args)
                    else:
                        raise BridgeError(f"Unknown direction: {direction}")
                self.send_json(200, {"ok": True, "log": buf.getvalue()})
                return

            self.send_json(404, {"error": "not found"})
        except Exception as exc:
            self.send_json(400, {"error": str(exc)})


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the local Save Sync PS-PC browser UI.")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8765)
    parser.add_argument("--games-dir", type=Path, default=DEFAULT_GAMES_DIR)
    args = parser.parse_args()

    Handler.games_dir = args.games_dir
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"Save Sync PS-PC UI: http://{args.host}:{args.port}/")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nStopping.")
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
