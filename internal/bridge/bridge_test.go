package bridge

import (
	"os"
	"path/filepath"
	"strings"
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
	backupDir, err := BackupCurrentSaves(filepath.Join(t.TempDir(), "backup"), "clair", pcDir, map[string][]byte{
		"gameplay":  []byte("ps5-main-original"),
		"container": []byte("ps5-container-original"),
	}, []gameapi.SaveImage{
		{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", PCFile: "EXPEDITION_0.sav", Payload: "ue4savegame.dpx.sav"},
		{Logical: "container", SaveName: "sdimg_SavesContainer", PCFile: "SavesContainer.sav", Payload: "ue4savegame.dpx.sav"},
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
