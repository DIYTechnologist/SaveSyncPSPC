package clair

import (
	"os"
	"path/filepath"
	"testing"

	"savesyncpspc/internal/gvas"
)

func TestConvertFromPS5ConvertsBothFiles(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, PCMain), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"), 522))
	mustWrite(t, filepath.Join(pcDir, PCContainer), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-menu"), 522))

	result, err := (Game{}).ConvertFromPS5(map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-menu"), 522),
	}, pcDir)
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, _ := gvas.Parse(result.Outputs[PCMain], "main")
	if got := result.Outputs[PCMain][mainInfo.PropertiesOffset:]; string(got) != "ps5-main" {
		t.Fatalf("main payload = %q", got)
	}
	containerInfo, _ := gvas.Parse(result.Outputs[PCContainer], "container")
	if got := result.Outputs[PCContainer][containerInfo.PropertiesOffset:]; string(got) != "ps5-menu" {
		t.Fatalf("container payload = %q", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestConvertToPS5ConvertsBothPayloads(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, PCMain), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"), 522))
	mustWrite(t, filepath.Join(pcDir, PCContainer), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-menu"), 522))

	result, err := (Game{}).ConvertToPS5(pcDir, map[string][]byte{
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
}

func TestInstallOutputsReusesBackupWithoutOverwriting(t *testing.T) {
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, PCMain), []byte("pc-main-original"))
	mustWrite(t, filepath.Join(pcDir, PCContainer), []byte("pc-container-original"))
	backupDir := filepath.Join(t.TempDir(), "backup", "clair-20260724112233", "PC")
	mustWrite(t, filepath.Join(backupDir, PCMain), []byte("central-backup-main"))
	mustWrite(t, filepath.Join(backupDir, PCContainer), []byte("central-backup-container"))

	err := (Game{}).InstallOutputs(map[string][]byte{
		PCMain:      []byte("converted-main"),
		PCContainer: []byte("converted-container"),
	}, pcDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(backupDir, PCMain)); string(got) != "central-backup-main" {
		t.Fatalf("backup overwritten: %q", got)
	}
	if got := mustRead(t, filepath.Join(pcDir, PCMain)); string(got) != "converted-main" {
		t.Fatalf("pc main = %q", got)
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
