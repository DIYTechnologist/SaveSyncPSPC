package bridge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"savesyncpspc/internal/garlic"

	"github.com/DIYTechnologist/savesync-engine/engine/larian"
	"github.com/DIYTechnologist/savesync-engine/gameapi"
)

func TestBackupCurrentSavesCreatesPCAndPS5Layout(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), []byte("pc-main-original"))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), []byte("pc-container-original"))
	when := time.Date(2026, 7, 24, 11, 22, 33, 0, time.UTC)
	backupDir, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "clair", pcDir, map[string][]byte{
		"gameplay":  []byte("ps5-main-original"),
		"container": []byte("ps5-container-original"),
	}, []gameapi.SaveImage{
		{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", PCFile: "EXPEDITION_0.sav", Payload: "ue4savegame.dpx.sav"},
		{Logical: "container", SaveName: "sdimg_SavesContainer", PCFile: "SavesContainer.sav", Payload: "ue4savegame.dpx.sav"},
	}, when, true)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(backupDir) != "clair-20260724112233" {
		t.Fatalf("backup dir = %s", backupDir)
	}
	if got := mustRead(t, filepath.Join(backupDir, "PC", "EXPEDITION_0.sav")); string(got) != "pc-main-original" {
		t.Fatalf("pc backup = %q", got)
	}
	if got := mustRead(t, filepath.Join(backupDir, "PS5", "sdimg_EXPEDITION0", "ue4savegame.dpx.sav")); string(got) != "ps5-main-original" {
		t.Fatalf("ps5 backup = %q", got)
	}
}

func TestBackupCurrentSavesSkipsMissingPCFileWhenNotRequired(t *testing.T) {
	pcDir := t.TempDir() // no PC save present yet - simulates a first-time ps5-to-pc sync
	backupDir, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "subnautica", pcDir, map[string][]byte{
		"slot0": []byte("ps5-original"),
	}, []gameapi.SaveImage{
		{Logical: "slot0", SaveName: "sdimg_slot0000", PCFile: "slot0000", Payload: "slot0000.blb"},
	}, time.Time{}, false)
	if err != nil {
		t.Fatalf("expected a missing PC file to be tolerated when requirePCFiles=false, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "PC", "slot0000")); !os.IsNotExist(err) {
		t.Fatal("expected no PC backup to be created for a PC file that never existed")
	}
}

func TestBackupCurrentSavesRequiresPCFileWhenRequired(t *testing.T) {
	pcDir := t.TempDir()
	_, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "subnautica", pcDir, map[string][]byte{
		"slot0": []byte("ps5-original"),
	}, []gameapi.SaveImage{
		{Logical: "slot0", SaveName: "sdimg_slot0000", PCFile: "slot0000", Payload: "slot0000.blb"},
	}, time.Time{}, true)
	if err == nil {
		t.Fatal("expected a missing PC file to error when requirePCFiles=true")
	}
}

func TestBackupCurrentSavesCopiesDirectoryShapedPCFile(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "slot0000", "gameinfo.json"), []byte(`{"protoBufVersion":13}`))
	mustWrite(t, filepath.Join(pcDir, "slot0000", "CellsCache", "baked-batch-cells-1-grp0.zip"), []byte("zip-bytes"))

	backupDir, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "subnautica", pcDir, map[string][]byte{
		"slot0": []byte("ps5-original"),
	}, []gameapi.SaveImage{
		{Logical: "slot0", SaveName: "sdimg_slot0000", PCFile: "slot0000", Payload: "slot0000.blb"},
	}, time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(backupDir, "PC", "slot0000", "gameinfo.json")); string(got) != `{"protoBufVersion":13}` {
		t.Fatalf("gameinfo.json backup = %q", got)
	}
	if got := mustRead(t, filepath.Join(backupDir, "PC", "slot0000", "CellsCache", "baked-batch-cells-1-grp0.zip")); string(got) != "zip-bytes" {
		t.Fatalf("CellsCache backup = %q", got)
	}
}

func TestSupportedGroupsRequireAllClairImages(t *testing.T) {
	saves := []garlic.Save{
		{"title_id": "PPSA17599", "save_name": "sdimg_EXPEDITION0", "type": "ps5", "backup": false, "usb": false, "uid": "user-a"},
		{"title_id": "PPSA17599", "save_name": "sdimg_SavesContainer", "type": "ps5", "backup": false, "usb": false, "uid": "user-a"},
		{"title_id": "PPSA17599", "save_name": "sdimg_BackupEXPEDITION0123", "type": "ps5", "backup": false, "usb": false, "uid": "user-a"},
		{"title_id": "PPSA17599", "save_name": "sdimg_EXPEDITION0", "type": "ps5", "backup": false, "usb": false, "uid": "missing-container"},
	}
	groups, err := SupportedGroups("../../games", saves)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %#v", groups)
	}
	if groups[0]["game"] != "clair" || groups[0]["uid"] != "user-a" {
		t.Fatalf("group = %#v", groups[0])
	}
}

func TestBuildOverridesExpandsAllowAll(t *testing.T) {
	overrides, err := BuildOverrides([]string{"a", "b", "c"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"a", "b", "c"} {
		if !overrides[token] {
			t.Fatalf("overrides = %#v, want %s set by --allow-all", overrides, token)
		}
	}
}

func TestBuildOverridesRejectsUnknownToken(t *testing.T) {
	_, err := BuildOverrides([]string{"a", "b"}, []string{"a", "bogus"}, false)
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
	if got := err.Error(); !strings.Contains(got, "bogus") || !strings.Contains(got, "a, b") {
		t.Fatalf("error = %q, want it to name the bad token and list valid ones", got)
	}
}

func TestBuildOverridesAcceptsKnownTokens(t *testing.T) {
	overrides, err := BuildOverrides([]string{"a", "b"}, []string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !overrides["a"] || overrides["b"] {
		t.Fatalf("overrides = %#v, want only a set", overrides)
	}
}

// fakeGarlicServer stands in for a real Garlic instance covering exactly
// what resolveDynamicImages needs: /api/saves (so FindSaveIndex/
// MountByName can locate the slot), /api/mount (returning a file
// listing), and /api/unmount. unmountCount lets tests assert the
// resolution pass always cleans up after itself.
func fakeGarlicServer(t *testing.T, mountFiles []string) (*httptest.Server, *int) {
	t.Helper()
	unmountCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/saves":
			fmt.Fprint(w, `{"saves":[{"title_id":"PPSA18463","save_name":"sdimg_Save0002","type":"ps5","uid":"u1"}]}`)
		case "/api/mount":
			var b strings.Builder
			b.WriteString(`{"files":[`)
			for i, name := range mountFiles {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"name":%q,"dir":false,"size":1}`, name)
			}
			b.WriteString(`]}`)
			fmt.Fprint(w, b.String())
		case "/api/unmount":
			unmountCount++
			fmt.Fprint(w, `{}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server, &unmountCount
}

func TestResolveDynamicImagesRequiresPS5SaveNameFlag(t *testing.T) {
	client := garlic.New("http://unused.test", time.Second)
	images := []gameapi.SaveImage{{Logical: "save", DynamicSaveName: true}}
	_, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, t.TempDir(), "", "", 0, "ps5-to-pc")
	if err == nil {
		t.Fatal("expected error when DynamicSaveName is set but PS5SaveName is empty")
	}
	if !strings.Contains(err.Error(), "--ps5-save-name") {
		t.Fatalf("error = %q, want it to mention --ps5-save-name", err)
	}
}

func TestResolveDynamicImagesResolvesPayloadViaMount(t *testing.T) {
	server, unmountCount := fakeGarlicServer(t, []string{"sce_sys/param.sfo", "A Nautiloid in Hell - 0h 00m.lsv"})
	client := garlic.New(server.URL, time.Second)

	images := []gameapi.SaveImage{{Logical: "save", DynamicSaveName: true, DynamicPayload: true}}
	resolved, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, t.TempDir(), "u1", "sdimg_Save0002", 0, "ps5-to-pc")
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0].SaveName != "sdimg_Save0002" {
		t.Fatalf("SaveName = %q", resolved[0].SaveName)
	}
	if resolved[0].Payload != "A Nautiloid in Hell - 0h 00m.lsv" {
		t.Fatalf("Payload = %q", resolved[0].Payload)
	}
	if *unmountCount != 1 {
		t.Fatalf("unmountCount = %d, want exactly 1 (resolution must not leave the save mounted)", *unmountCount)
	}
}

// TestResolveDynamicImagesRejectsPathTraversalInPayload is a regression
// test: image.Payload for a DynamicPayload image comes straight from
// Garlic's own mount-listing response (see fakeGarlicServer) - network
// input from the PS5 device, not something this tool generates - and
// later gets joined via filepath.Join into backup/output paths. A
// malicious or compromised Garlic response naming a file like
// "../../evil.lsv" must be rejected here rather than allowed to escape
// the intended directory on write.
func TestResolveDynamicImagesRejectsPathTraversalInPayload(t *testing.T) {
	server, _ := fakeGarlicServer(t, []string{"../../evil.lsv"})
	client := garlic.New(server.URL, time.Second)

	images := []gameapi.SaveImage{{Logical: "save", DynamicSaveName: true, DynamicPayload: true}}
	_, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, t.TempDir(), "u1", "sdimg_Save0002", 0, "ps5-to-pc")
	if err == nil {
		t.Fatal("expected a path-traversal payload name to be rejected")
	}
}

// TestResolveDynamicImagesRejectsPathTraversalInSaveName covers the same
// escape via --ps5-save-name: although that flag is normally the local
// user's own trusted input, resolveDynamicImages joins it into
// filesystem paths the same way, so it must be validated too.
func TestResolveDynamicImagesRejectsPathTraversalInSaveName(t *testing.T) {
	client := garlic.New("http://unused.test", time.Second)
	images := []gameapi.SaveImage{{Logical: "save", DynamicSaveName: true}}
	_, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, t.TempDir(), "", "../../etc", 0, "ps5-to-pc")
	if err == nil {
		t.Fatal("expected a path-traversal save name to be rejected")
	}
}

func TestResolveDynamicImagesResolvesPCFileOnlyForPCToPS5(t *testing.T) {
	server, _ := fakeGarlicServer(t, []string{"save.lsv"})
	client := garlic.New(server.URL, time.Second)
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "Ruined Battlefield - 39h 05m.lsv"), []byte("x"))

	images := []gameapi.SaveImage{{Logical: "save", DynamicPCFile: true}}

	resolvedForPC, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, pcDir, "u1", "sdimg_Save0002", 0, "pc-to-ps5")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedForPC[0].PCFile != "Ruined Battlefield - 39h 05m.lsv" {
		t.Fatalf("pc-to-ps5 PCFile = %q, want it resolved", resolvedForPC[0].PCFile)
	}

	resolvedForPS5, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA18463"}, images, pcDir, "u1", "sdimg_Save0002", 0, "ps5-to-pc")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedForPS5[0].PCFile != "" {
		t.Fatalf("ps5-to-pc PCFile = %q, want it left unresolved (engine names its own output)", resolvedForPS5[0].PCFile)
	}
}

func TestResolveDynamicImagesLeavesStaticImagesUntouched(t *testing.T) {
	client := garlic.New("http://unused.test", time.Second)
	images := []gameapi.SaveImage{{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", PCFile: "EXPEDITION_0.sav", Payload: "ue4savegame.dpx.sav"}}
	resolved, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA17599"}, images, t.TempDir(), "", "", 0, "pc-to-ps5")
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0] != images[0] {
		t.Fatalf("resolved = %#v, want unchanged from input for a fully-static image", resolved[0])
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestSteamAccountIDNormalizesSteamID64 pins the fix for a live-confirmed
// failure (RE3, 2026-08-06): RE2/RE3 PC saves embed the 32-bit Steam
// account id, and a save carrying a SteamID64 there instead is silently
// omitted from the game's load list - an apparently empty slot, with no
// error anywhere to explain it. --steam-id accepts either form, so this
// normalization is what makes both produce the value real saves hold.
func TestSteamAccountIDNormalizesSteamID64(t *testing.T) {
	const accountID = uint64(11052978)
	// SteamID64 for an individual account: 0x1100001_00000000 + account id.
	const steamID64 = uint64(76561197960265728) + accountID

	for _, tc := range []struct {
		name  string
		input uint64
		want  uint64
	}{
		{"SteamID64 is reduced to its account id", steamID64, accountID},
		{"an account id is left alone", accountID, accountID},
		{"zero stays zero so 'unset' stays detectable", 0, 0},
	} {
		if got := steamAccountID(tc.input); got != tc.want {
			t.Errorf("%s: steamAccountID(%d) = %d, want %d", tc.name, tc.input, got, tc.want)
		}
	}
}

// TestResolveDynamicImagesNormalizesSteamID checks the normalization is
// actually wired into the path engines receive, not just available as a
// helper - resolveDynamicImages is the single choke point both the CLI
// and the UI server pass through.
func TestResolveDynamicImagesNormalizesSteamID(t *testing.T) {
	client := garlic.New("http://unused.test", time.Second)
	images := []gameapi.SaveImage{{Logical: "save", SaveName: "sdimg_EXPEDITION0", PCFile: "x.sav", Payload: "y.sav"}}
	const steamID64 = uint64(76561197971318706)

	resolved, err := resolveDynamicImages(client, larian.New(), nil, []string{"PPSA17599"}, images, t.TempDir(), "", "", steamID64, "pc-to-ps5")
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0].SteamID != 11052978 {
		t.Fatalf("SteamID = %d, want it normalized to the 32-bit account id 11052978", resolved[0].SteamID)
	}
}
