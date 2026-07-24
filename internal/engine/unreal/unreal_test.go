package unreal

import (
	"os"
	"path/filepath"
	"testing"

	"savesyncpspc/internal/gvas"
)

// clairLikeConfig mirrors games/clair.json's engine_config, the way it's
// actually shipped, so these tests stand in for the deleted
// internal/games/clair test suite: the same conversion behavior driven
// through profile data instead of a hardcoded Go plugin.
func clairLikeConfig() Config {
	return Config{
		Module: "Sandfall",
		Images: []ImageConfig{
			{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", Label: "EXPEDITION_0", PCFile: "EXPEDITION_0.sav", Payload: "ue4savegame.dpx.sav"},
			{Logical: "container", SaveName: "sdimg_SavesContainer", Label: "SavesContainer", PCFile: "SavesContainer.sav", Payload: "ue4savegame.dpx.sav"},
		},
		ClassEquivalence: []ClassEquivalence{
			{
				Logical:  "gameplay",
				PC:       "/Script/Sandfall.BP_SaveGameObject_V8_C",
				PS5:      "/Script/Sandfall.BP_SaveGameObject_V7_C",
				Verified: true,
				Note:     "Known compatible envelope graft: Steam gameplay V8 <-> PS5 gameplay V7.",
			},
		},
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

func TestParseConfigValidatesImages(t *testing.T) {
	if _, err := (Engine{}).ParseConfig([]byte(`{"module":"Sandfall","images":[]}`)); err == nil {
		t.Fatal("expected error for empty images")
	}
	if _, err := (Engine{}).ParseConfig([]byte(`{"module":"Sandfall","images":[{"logical":"gameplay"}]}`)); err == nil {
		t.Fatal("expected error for incomplete image entry")
	}
	cfg, err := (Engine{}).ParseConfig([]byte(`{
		"module": "Sandfall",
		"images": [{"logical":"gameplay","save_name":"sdimg_EXPEDITION0","pc_file":"EXPEDITION_0.sav","payload":"ue4savegame.dpx.sav"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.(Config); !ok {
		t.Fatalf("ParseConfig returned %T, want Config", cfg)
	}
}

func TestConvertFromPS5ConvertsBothFiles(t *testing.T) {
	cfg := clairLikeConfig()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-menu"), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-menu"), 522),
	}, pcDir)
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, _ := gvas.Parse(result.Outputs["EXPEDITION_0.sav"], "main")
	if got := result.Outputs["EXPEDITION_0.sav"][mainInfo.PropertiesOffset:]; string(got) != "ps5-main" {
		t.Fatalf("main payload = %q", got)
	}
	containerInfo, _ := gvas.Parse(result.Outputs["SavesContainer.sav"], "container")
	if got := result.Outputs["SavesContainer.sav"][containerInfo.PropertiesOffset:]; string(got) != "ps5-menu" {
		t.Fatalf("container payload = %q", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestConvertToPS5ConvertsBothPayloads(t *testing.T) {
	cfg := clairLikeConfig()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-menu"), 522))

	result, err := (Engine{}).ConvertToPS5(cfg, pcDir, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-menu"), 522),
	})
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, _ := gvas.Parse(result.Outputs["sdimg_EXPEDITION0"], "main")
	if got := result.Outputs["sdimg_EXPEDITION0"][mainInfo.PropertiesOffset:]; string(got) != "pc-main" {
		t.Fatalf("main payload = %q", got)
	}
	containerInfo, _ := gvas.Parse(result.Outputs["sdimg_SavesContainer"], "container")
	if got := result.Outputs["sdimg_SavesContainer"][containerInfo.PropertiesOffset:]; string(got) != "pc-menu" {
		t.Fatalf("container payload = %q", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestClassMismatchWarnsWithoutBlocking(t *testing.T) {
	cfg := clairLikeConfig()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V9_C", []byte("pc-main"), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-menu"), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-menu"), 522),
	}, pcDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly 1 (unmapped PC class V9)", result.Warnings)
	}
}

func TestInstallOutputsReusesBackupWithoutOverwriting(t *testing.T) {
	cfg := clairLikeConfig()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), []byte("pc-main-original"))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), []byte("pc-container-original"))
	backupDir := filepath.Join(t.TempDir(), "backup", "clair-20260724112233", "PC")
	mustWrite(t, filepath.Join(backupDir, "EXPEDITION_0.sav"), []byte("central-backup-main"))
	mustWrite(t, filepath.Join(backupDir, "SavesContainer.sav"), []byte("central-backup-container"))

	err := (Engine{}).InstallOutputs(cfg, map[string][]byte{
		"EXPEDITION_0.sav":   []byte("converted-main"),
		"SavesContainer.sav": []byte("converted-container"),
	}, pcDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(backupDir, "EXPEDITION_0.sav")); string(got) != "central-backup-main" {
		t.Fatalf("backup overwritten: %q", got)
	}
	if got := mustRead(t, filepath.Join(pcDir, "EXPEDITION_0.sav")); string(got) != "converted-main" {
		t.Fatalf("pc main = %q", got)
	}
}
