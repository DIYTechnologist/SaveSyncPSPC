package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"savesyncpspc/internal/bridge"
	"savesyncpspc/internal/games"
	"savesyncpspc/internal/garlic"
	"savesyncpspc/internal/util"
)

type Server struct {
	GamesDir string
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/games", s.apiGames)
	mux.HandleFunc("/api/saves", s.apiSaves)
	mux.HandleFunc("/api/users", s.apiUsers)
	mux.HandleFunc("/api/run", s.apiRun)
	return mux
}

func (s Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func (s Server) apiGames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	profiles, err := games.Profiles(s.GamesDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var out []map[string]any
	for _, profile := range profiles {
		compatibility := any(nil)
		if eng, cfg, err := games.ResolveEngine(profile); err == nil {
			compatibility = eng.Compatibility(cfg)
		}
		out = append(out, map[string]any{
			"key":           profile.Key,
			"name":          profile.Name,
			"ids":           profile.TitleIDs,
			"compatibility": compatibility,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": out})
}

func (s Server) apiSaves(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	req, err := readJSON(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	client, err := newGarlicClient(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	users, userErr := client.Users()
	if userErr != nil {
		users = []garlic.User{{"id": "", "name": "Unable to load users: " + userErr.Error()}}
	}
	saves, err := client.Saves()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	profiles, _ := games.Profiles(s.GamesDir)
	byID := map[string]string{}
	for _, profile := range profiles {
		for _, id := range profile.TitleIDs {
			byID[strings.ToUpper(id)] = profile.Key
		}
	}
	userNames := map[string]string{}
	for _, user := range users {
		id := fmt.Sprint(user["id"])
		if id != "" {
			userNames[id] = fmt.Sprint(user["name"])
		}
	}
	for idx, save := range saves {
		titleID := strings.ToUpper(fmt.Sprint(save["title_id"]))
		save["supported_game"] = byID[titleID]
		save["idx"] = idx
		save["user_name"] = userNames[fmt.Sprint(save["uid"])]
	}
	groups, err := bridge.SupportedGroups(s.GamesDir, saves)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saves": saves, "groups": groups, "users": users})
}

func (s Server) apiUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	req, err := readJSON(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	client, err := newGarlicClient(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	users, err := client.Users()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s Server) apiRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	req, err := readJSON(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := validateGarlicURL(fmt.Sprint(req["garlic"])); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	pcDir := strings.TrimSpace(fmt.Sprint(req["pc_dir"]))
	install := boolValue(req["install"])
	apply := boolValue(req["apply"])
	if pcDir == "" && !install && !apply {
		log, err := s.validateGroupOnly(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": log})
		return
	}
	var buf bytes.Buffer
	options := bridge.Options{
		GarlicURL:  fmt.Sprint(req["garlic"]),
		Timeout:    timeout(req),
		PS5UID:     stringValue(req["ps5_uid"]),
		GamesDir:   s.GamesDir,
		Game:       stringValue(req["game"]),
		TitleID:    stringValue(req["title_id"]),
		BackupRoot: defaultString(stringValue(req["backup_root"]), "backup"),
		PCDir:      pcDir,
		OutputDir:  stringValue(req["output_dir"]),
		Force:      boolValueDefault(req["force"], true),
		Install:    install,
		Apply:      apply,
		Yes:        boolValue(req["yes"]),
		Log: func(message string) {
			buf.WriteString(message)
			buf.WriteByte('\n')
		},
	}
	switch fmt.Sprint(req["direction"]) {
	case "ps5-to-pc":
		err = bridge.PS5ToPC(options)
	case "pc-to-ps5":
		err = bridge.PCToPS5(options)
	default:
		err = fmt.Errorf("unknown direction: %v", req["direction"])
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": buf.String()})
}

func (s Server) validateGroupOnly(req map[string]any) (string, error) {
	client, err := newGarlicClient(req)
	if err != nil {
		return "", err
	}
	saves, err := client.Saves()
	if err != nil {
		return "", err
	}
	groups, err := bridge.SupportedGroups(s.GamesDir, saves)
	if err != nil {
		return "", err
	}
	game := stringValue(req["game"])
	uid := stringValue(req["ps5_uid"])
	var filtered []map[string]any
	for _, group := range groups {
		if game != "" && group["game"] != game {
			continue
		}
		if uid != "" && group["uid"] != uid {
			continue
		}
		filtered = append(filtered, group)
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("no complete supported save group matched the selected game/user")
	}
	if len(filtered) > 1 {
		return "", fmt.Errorf("multiple groups matched. Select a PS5 user first")
	}
	group := filtered[0]
	return fmt.Sprintf("Dry run validation OK.\nSelected: %s / %s / uid %s\nRequired Garlic save images are present.\n\nNo PC save directory was supplied, so no conversion was attempted.", group["game_name"], group["title_id"], group["uid"]), nil
}

// newGarlicClient builds a Garlic client from a browser-supplied request,
// validating the target URL first. req["garlic"] is taken verbatim from an
// unauthenticated HTTP endpoint (see validateGarlicURL for why that matters).
func newGarlicClient(req map[string]any) (*garlic.Client, error) {
	raw := fmt.Sprint(req["garlic"])
	if err := validateGarlicURL(raw); err != nil {
		return nil, err
	}
	return garlic.New(raw, timeout(req)), nil
}

// validateGarlicURL rejects Garlic URLs that would let this server be used
// as an open proxy against link-local/metadata addresses. The UI has no
// auth, so anything reachable on its port can otherwise ask this process to
// issue arbitrary GET/POST requests (e.g. to a cloud metadata endpoint) on
// its behalf. Ordinary LAN/loopback targets, which is what this tool is
// for, are left untouched.
func validateGarlicURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid garlic URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("garlic URL must use http or https")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("garlic URL is missing a host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// Not resolvable here; let the HTTP client surface the real error.
		return nil
	}
	for _, ip := range ips {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("refusing to contact link-local address %s (e.g. cloud metadata endpoints); point --garlic at your PS5's LAN address", ip)
		}
	}
	return nil
}

func readJSON(r *http.Request) (map[string]any, error) {
	defer r.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func timeout(req map[string]any) time.Duration {
	value := 120.0
	switch typed := req["timeout"].(type) {
	case float64:
		value = typed
	case string:
		if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
			value = parsed
		}
	}
	return time.Duration(value * float64(time.Second))
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolValue(value any) bool {
	return util.BoolValue(value, false)
}

func boolValueDefault(value any, fallback bool) bool {
	return util.BoolValue(value, fallback)
}

const indexHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Save Sync PS-PC</title>
  <style>
    :root { --bg:#101114; --panel:#181a20; --panel2:#20232b; --bd:#343844; --tx:#e9edf5; --mut:#9ba4b5; --ac:#4da3ff; --ok:#37d67a; --er:#ff5c6c; }
    * { box-sizing:border-box } body { margin:0; background:var(--bg); color:var(--tx); font-family:system-ui,-apple-system,Segoe UI,sans-serif; }
    header { height:56px; display:flex; align-items:center; gap:14px; padding:0 18px; background:#15171d; border-bottom:1px solid var(--bd); }
    h1 { font-size:18px; margin:0; font-weight:700 } main { display:grid; grid-template-columns:360px 1fr; gap:16px; padding:16px; min-height:calc(100vh - 56px); }
    section { background:var(--panel); border:1px solid var(--bd); border-radius:8px; padding:14px; }
    label { display:block; color:var(--mut); font-size:12px; margin:12px 0 5px } input, select { width:100%; background:var(--panel2); color:var(--tx); border:1px solid var(--bd); border-radius:6px; padding:9px 10px; font:inherit; font-size:14px; }
    .row { display:grid; grid-template-columns:1fr 1fr; gap:10px } button { background:var(--ac); color:#07111f; border:0; border-radius:6px; padding:9px 12px; font-weight:700; cursor:pointer; margin-top:12px; }
    button.secondary { background:var(--panel2); color:var(--tx); border:1px solid var(--bd) } button.danger { background:var(--er); color:#fff }
    .actions { display:flex; gap:8px; flex-wrap:wrap } .save { display:grid; grid-template-columns:120px 1fr 120px 110px; gap:8px; border-bottom:1px solid var(--bd); padding:9px 0; align-items:center; font-size:13px; cursor:pointer; }
    .save:first-child { border-top:1px solid var(--bd) } .save:hover { background:#1d2129 } .save.selected { background:#1b3047; outline:1px solid var(--ac) }
    .filters { display:grid; grid-template-columns:1fr 1fr 1fr; gap:10px; margin-bottom:10px } .checkrow { display:flex; align-items:center; gap:8px; color:var(--mut); font-size:13px; margin:10px 0 } .checkrow input { width:auto }
    .mut { color:var(--mut) } .pill { display:inline-block; padding:2px 7px; border-radius:999px; background:#283142; color:#bcd7ff; font-size:11px; font-weight:700; }
    pre { background:#0a0b0e; border:1px solid var(--bd); border-radius:8px; padding:12px; overflow:auto; white-space:pre-wrap; min-height:220px; color:#c8d2e2; }
    .ok { color:var(--ok) } .err { color:var(--er) } @media (max-width: 860px) { main { grid-template-columns:1fr } .save { grid-template-columns:1fr } }
  </style>
</head>
<body>
  <header><h1>Save Sync PS-PC</h1><span class="mut">PC bridge for PlayStation saves</span></header>
  <main>
    <section>
      <label>Garlic URL</label><input id="garlic" value="http://192.168.2.67:8082">
      <div class="row"><div><label>Game</label><select id="game" onchange="updateCompatibility()"></select><div id="compat" class="mut" style="font-size:12px;margin-top:6px"></div></div><div><label>PS5 User</label><select id="uid"><option value="">Auto / all users</option></select></div></div>
      <label>PC save directory</label><input id="pcdir" placeholder="/path/to/SaveGames or C:\Users\...\SaveGames">
      <label>Output directory</label><input id="outdir" value="./garlic_ui_output">
      <div class="row"><div><label>Direction</label><select id="direction"><option value="ps5-to-pc">PS5 to PC</option><option value="pc-to-ps5">PC to PS5</option></select></div><div><label>Timeout seconds</label><input id="timeout" value="120"></div></div>
      <div class="actions"><button onclick="loadSaves()">Get Saves</button><button onclick="runSync(false)">Validate / Convert</button><button class="danger" onclick="runSync(true)">Apply to PS5</button></div>
      <div class="actions"><button class="secondary" onclick="runInstall()">Install to PC Dir</button></div>
      <p class="mut">Every conversion creates backup/&lt;game&gt;-&lt;timestamp&gt;/PC and PS5 before conversion or upload.</p>
    </section>
    <section><h2 style="font-size:15px;margin:0 0 10px">Garlic Saves</h2><div class="filters"><div><label style="margin-top:0">Filter user</label><select id="filterUser" onchange="renderSaves()"><option value="">All users</option></select></div><div><label style="margin-top:0">Filter game</label><select id="filterGame" onchange="renderSaves()"><option value="">All games</option></select></div><div><label style="margin-top:0">Search</label><input id="filterText" oninput="renderSaves()" placeholder="title, save, uid"></div></div><label class="checkrow"><input type="checkbox" id="showRaw" onchange="renderSaves()"> Show raw Garlic saves</label><div id="saves" class="mut">Click Get Saves.</div><h2 style="font-size:15px;margin:18px 0 10px">Log</h2><pre id="log"></pre></section>
  </main>
<script>
const $ = id => document.getElementById(id); let allSaves = [], allGroups = [], allUsers = [], allGames = [], selectedGroupKey = '';
function esc(s){return String(s ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function log(text){const el=$('log'); el.textContent += text + '\n'; el.scrollTop = el.scrollHeight;}
async function api(path, body){const res=await fetch(path,{method:body?'POST':'GET',headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined}); const data=await res.json(); if(!res.ok||data.error) throw new Error(data.error||res.statusText); return data;}
async function loadGames(){try{const data=await api('/api/games'); allGames=data.games||[]; const options=allGames.map(g=>` + "`" + `<option value="${esc(g.key)}">${esc(g.name)} (${esc(g.ids.join(', '))})</option>` + "`" + `).join(''); $('game').innerHTML=options; $('filterGame').innerHTML='<option value="">All games</option>'+options; updateCompatibility();}catch(e){log('Games error: '+e.message);}}
function updateCompatibility(){const game=allGames.find(g=>g.key===$('game').value); if(!game||!game.compatibility){$('compat').textContent=''; return;} const c=game.compatibility; $('compat').textContent=` + "`" + `${c.pc.platform} ${c.pc.version} <-> ${c.ps5.platform} ${c.ps5.version}: ${c.convertible ? 'convertible' : 'not marked convertible'}` + "`" + `;}
function userLabel(uid){const user=allUsers.find(u=>u.id===uid); return user?` + "`" + `${user.name} (${user.id})` + "`" + `:(uid||'unknown');}
function populateUsers(users,saves){const byId=new Map(); for(const user of users||[]){if(user.id) byId.set(user.id,user.name||user.id);} for(const save of saves||[]){if(save.uid&&!byId.has(save.uid)) byId.set(save.uid,save.uid);} allUsers=Array.from(byId,([id,name])=>({id,name})); const opts=allUsers.map(u=>` + "`" + `<option value="${esc(u.id)}">${esc(u.name)} (${esc(u.id)})</option>` + "`" + `).join(''); $('uid').innerHTML='<option value="">Auto / all users</option>'+opts; $('filterUser').innerHTML='<option value="">All users</option>'+opts;}
async function loadSaves(){ $('saves').textContent='Loading...'; try{const data=await api('/api/saves',{garlic:$('garlic').value,timeout:Number($('timeout').value||120)}); allSaves=data.saves||[]; allGroups=data.groups||[]; populateUsers(data.users||[],allSaves); renderSaves(); log('Loaded '+allSaves.length+' raw saves, '+allGroups.length+' supported groups and '+allUsers.length+' users from Garlic.');}catch(e){$('saves').innerHTML=` + "`" + `<span class="err">${e.message}</span>` + "`" + `; log('Saves error: '+e.message);}}
function selectGroup(i){const group=allGroups[i]; if(!group)return; selectedGroupKey=group.key; if(group.uid){$('uid').value=group.uid; $('filterUser').value=group.uid;} if(group.game){$('game').value=group.game; $('filterGame').value=group.game; updateCompatibility();} renderSaves(); log('Selected '+group.game_name+' / '+group.title_id+' / '+userLabel(group.uid));}
function renderSaves(){if($('showRaw').checked){renderRawSaves(); return;} const user=$('filterUser').value, game=$('filterGame').value, q=$('filterText').value.trim().toLowerCase(); const rows=allGroups.map((g,i)=>({g,i})).filter(({g})=>!user||g.uid===user).filter(({g})=>!game||g.game===game).filter(({g})=>!q||[g.title_id,g.title_name,g.uid,g.game,g.game_name,(g.images||[]).map(x=>x.save_name).join(' ')].some(v=>String(v||'').toLowerCase().includes(q))); if(!rows.length){$('saves').innerHTML='<span class="mut">No complete supported save groups match the current filters.</span>'; return;} $('saves').innerHTML=rows.map(({g,i})=>{const selected=g.key===selectedGroupKey?' selected':''; const imageText=(g.images||[]).map(x=>x.save_name).join(' + '); return ` + "`" + `<div class="save${selected}" onclick="selectGroup(${i})"><div><span class="pill">${esc(g.game)}</span><br><span class="mut">${esc(userLabel(g.uid))}</span></div><div><b>${esc(g.game_name)}</b><br><span class="mut">${esc(g.title_id)} / ${esc(imageText)}</span></div><div class="ok">complete</div><div class="mut">${(g.images||[]).length} images</div></div>` + "`" + `;}).join('');}
function renderRawSaves(){const user=$('filterUser').value, game=$('filterGame').value, q=$('filterText').value.trim().toLowerCase(); const rows=allSaves.filter(s=>(!user||s.uid===user)&&(!game||s.supported_game===game)&&(!q||[s.title_id,s.title_name,s.save_name,s.uid,s.path,s.supported_game].some(v=>String(v||'').toLowerCase().includes(q)))); if(!rows.length){$('saves').innerHTML='<span class="mut">No raw saves match the current filters.</span>'; return;} $('saves').innerHTML=rows.map(s=>{const flags=[s.backup?'backup':'',s.usb?'usb':''].filter(Boolean).join(' '); return ` + "`" + `<div class="save"><div><span class="pill">${esc(s.type||'')}</span><br><span class="mut">${esc(userLabel(s.uid))}</span></div><div><b>${esc(s.title_name||s.title_id)}</b><br><span class="mut">${esc(s.title_id)} / ${esc(s.save_name)}</span></div><div>${s.supported_game?esc(s.supported_game):'<span class="mut">unsupported</span>'}</div><div class="mut">${esc(flags)}</div></div>` + "`" + `;}).join('');}
function requestBase(){return {garlic:$('garlic').value,game:$('game').value,ps5_uid:$('uid').value||null,pc_dir:$('pcdir').value.trim(),output_dir:$('outdir').value,timeout:Number($('timeout').value||120),force:true};}
async function runSync(apply){const body=requestBase(); body.direction=$('direction').value; body.apply=apply; body.yes=apply; body.install=false; try{log('Running '+body.direction+(apply?' with PS5 apply...':'...')); const data=await api('/api/run',body); log(data.log||'Done.');}catch(e){log('Run error: '+e.message);}}
async function runInstall(){const body=requestBase(); body.direction='ps5-to-pc'; body.install=true; body.apply=false; body.yes=false; try{log('Running PS5 to PC with install...'); const data=await api('/api/run',body); log(data.log||'Done.');}catch(e){log('Install error: '+e.message);}}
loadGames();
</script></body></html>`
