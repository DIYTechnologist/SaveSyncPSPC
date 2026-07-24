package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/garlic"
)

func TestBackupCurrentSavesCreatesPCAndPS5Layout(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), []byte("pc-main-original"))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), []byte("pc-container-original"))
	when := time.Date(2026, 7, 24, 11, 22, 33, 0, time.UTC)
	backupDir, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "clair", pcDir, "ue4savegame.dpx.sav", map[string][]byte{
		"gameplay":  []byte("ps5-main-original"),
		"container": []byte("ps5-container-original"),
	}, []gameapi.SaveImage{
		{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", PCFile: "EXPEDITION_0.sav"},
		{Logical: "container", SaveName: "sdimg_SavesContainer", PCFile: "SavesContainer.sav"},
	}, when)
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
