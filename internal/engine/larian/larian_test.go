package larian

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
)

func TestInspectPortableWithRequiredMembers(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "meta.lsf", content: []byte("meta"), compression: CompressionNone},
		{name: "SaveInfo.json", content: []byte(`{"Platform":"Steam"}`), compression: CompressionZlib},
		{name: "StorySave.bin", content: []byte("story"), compression: CompressionNone},
		{name: "Globals.lsf", content: []byte("globals"), compression: CompressionZlib},
	})

	verdict := (Engine{}).Inspect(nil, "gameplay", data, engine.SidePS5, nil)
	if !verdict.Portable {
		t.Fatalf("checks = %#v, want portable", verdict.Checks)
	}
}

func TestInspectBlocksMissingRequiredMember(t *testing.T) {
	data := buildSyntheticArchive(t, []syntheticEntry{
		{name: "meta.lsf", content: []byte("meta"), compression: CompressionNone},
		// SaveInfo.json, StorySave.bin, Globals.lsf all missing.
	})

	verdict := (Engine{}).Inspect(nil, "gameplay", data, engine.SidePS5, nil)
	if verdict.Portable {
		t.Fatal("expected not portable: required members missing")
	}
	if verdict.Tier != engine.TierWrongFormat {
		t.Fatalf("tier = %v, want TierWrongFormat", verdict.Tier)
	}
}

func TestInspectRejectsNonLSPKPayload(t *testing.T) {
	verdict := (Engine{}).Inspect(nil, "gameplay", []byte("not an lspk file"), engine.SidePS5, nil)
	if verdict.Portable {
		t.Fatal("expected not portable: not an LSPK archive")
	}
	if len(verdict.Checks) != 1 || verdict.Checks[0].Check != CheckMagic {
		t.Fatalf("checks = %#v, want a single magic check", verdict.Checks)
	}
}

func TestParseConfigValidation(t *testing.T) {
	if _, err := (Engine{}).ParseConfig([]byte(`{"images":[]}`)); err == nil {
		t.Fatal("expected error for empty images")
	}
	if _, err := (Engine{}).ParseConfig([]byte(`{"images":[{"label":"no logical"}]}`)); err == nil {
		t.Fatal("expected error for missing logical")
	}
	cfg, err := (Engine{}).ParseConfig([]byte(`{"images":[{"logical":"save","dynamic_save_name":true,"dynamic_payload":true,"dynamic_pc_file":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.(Config); !ok {
		t.Fatalf("ParseConfig returned %T, want Config", cfg)
	}
}

func TestImagesCarriesDynamicFlags(t *testing.T) {
	cfg := Config{Images: []ImageConfig{{Logical: "save", DynamicSaveName: true, DynamicPayload: true, DynamicPCFile: true}}}
	images := (Engine{}).Images(cfg)
	if len(images) != 1 {
		t.Fatalf("images = %#v", images)
	}
	img := images[0]
	if !img.DynamicSaveName || !img.DynamicPayload || !img.DynamicPCFile {
		t.Fatalf("image = %#v, want all Dynamic* flags set", img)
	}
}

func TestResolvePayloadFindsSingleLSV(t *testing.T) {
	name, err := (Engine{}).ResolvePayload(nil, gameapi.SaveImage{Logical: "save"}, []string{
		"sce_sys", "sce_sys/param.sfo", "A Nautiloid in Hell - 0h 00m.lsv", "A Nautiloid in Hell - 0h 00m.WebP",
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "A Nautiloid in Hell - 0h 00m.lsv" {
		t.Fatalf("name = %q", name)
	}
}

func TestResolvePayloadErrorsOnZeroMatches(t *testing.T) {
	if _, err := (Engine{}).ResolvePayload(nil, gameapi.SaveImage{Logical: "save"}, []string{"sce_sys/param.sfo"}); err == nil {
		t.Fatal("expected error when no .lsv file is present")
	}
}

func TestResolvePayloadErrorsOnMultipleMatches(t *testing.T) {
	if _, err := (Engine{}).ResolvePayload(nil, gameapi.SaveImage{Logical: "save"}, []string{"a.lsv", "b.lsv"}); err == nil {
		t.Fatal("expected error when multiple .lsv files are present")
	}
}

func TestResolvePCFileFindsSingleLSV(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "Ruined Battlefield - 39h 05m.lsv"), []byte("x"))
	mustWriteFile(t, filepath.Join(dir, "Ruined Battlefield - 39h 05m.WebP"), []byte("y"))

	name, err := (Engine{}).ResolvePCFile(nil, gameapi.SaveImage{Logical: "save"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Ruined Battlefield - 39h 05m.lsv" {
		t.Fatalf("name = %q", name)
	}
}

func TestResolvePCFileErrorsOnMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.lsv"), []byte("x"))
	mustWriteFile(t, filepath.Join(dir, "b.lsv"), []byte("y"))

	if _, err := (Engine{}).ResolvePCFile(nil, gameapi.SaveImage{Logical: "save"}, dir); err == nil {
		t.Fatal("expected error when multiple .lsv files are present")
	}
}

func TestResolvePCFileErrorsOnNoMatches(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Engine{}).ResolvePCFile(nil, gameapi.SaveImage{Logical: "save"}, dir); err == nil {
		t.Fatal("expected error when no .lsv file is present")
	}
}

// syntheticSaveInfo builds a minimal BG3-shaped LSPK archive whose
// SaveInfo.json has the given Platform value.
func syntheticSaveInfo(platform string) []byte {
	return []byte(`{
   "Platform" : "` + platform + `",
   "Save Name" : "Test"
}`)
}

func syntheticBG3Archive(t *testing.T, platform string) []byte {
	t.Helper()
	return buildSyntheticArchive(t, []syntheticEntry{
		{name: "meta.lsf", content: bytes.Repeat([]byte{0xAB}, 200), compression: CompressionZlib},
		{name: "SaveInfo.json", content: syntheticSaveInfo(platform), compression: CompressionZlib},
		{name: "StorySave.bin", content: bytes.Repeat([]byte{0x11}, 500), compression: CompressionZlib},
		{name: "Globals.lsf", content: bytes.Repeat([]byte{0x22}, 300), compression: CompressionZlib},
	})
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConvertToPS5RewritesPlatformOnly is the unit-level counterpart to
// the real, live-confirmed PC->PS5 graft (see docs/dev.md): every entry's
// content survives the round-trip unchanged except SaveInfo.json's
// Platform field.
func TestConvertToPS5RewritesPlatformOnly(t *testing.T) {
	pcDir := t.TempDir()
	pcData := syntheticBG3Archive(t, "Steam")
	mustWriteFile(t, filepath.Join(pcDir, "MySave.lsv"), pcData)

	images := []gameapi.SaveImage{{Logical: "save", PCFile: "MySave.lsv", SaveName: "sdimg_Save0002", Payload: "MySave.lsv"}}
	result, err := (Engine{}).ConvertToPS5(nil, images, pcDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, ok := result.Outputs["sdimg_Save0002"]
	if !ok {
		t.Fatalf("outputs = %#v, want key sdimg_Save0002", result.Outputs)
	}

	archive, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := Parse(pcData)
	for _, e := range original.Entries {
		found, ok := archive.Find(e.Name)
		if !ok {
			t.Fatalf("%s missing from rebuilt archive", e.Name)
		}
		got, err := archive.ReadDecompressed(found)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := original.ReadDecompressed(e)
		if e.Name == "SaveInfo.json" {
			if bytes.Equal(got, want) {
				t.Fatal("SaveInfo.json was not rewritten")
			}
			if !bytes.Contains(got, []byte(`"Platform" : "Prospero"`)) {
				t.Fatalf("SaveInfo.json = %s, want Platform rewritten to Prospero", got)
			}
			continue
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s content changed unexpectedly", e.Name)
		}
	}
}

func TestConvertToPS5RequiresResolvedNames(t *testing.T) {
	pcDir := t.TempDir()
	// PCFile unresolved (empty).
	if _, err := (Engine{}).ConvertToPS5(nil, []gameapi.SaveImage{{Logical: "save", SaveName: "sdimg_Save0002"}}, pcDir, nil, nil); err == nil {
		t.Fatal("expected error when PCFile is unresolved")
	}
	// SaveName unresolved (empty).
	mustWriteFile(t, filepath.Join(pcDir, "x.lsv"), []byte("x"))
	if _, err := (Engine{}).ConvertToPS5(nil, []gameapi.SaveImage{{Logical: "save", PCFile: "x.lsv"}}, pcDir, nil, nil); err == nil {
		t.Fatal("expected error when SaveName is unresolved")
	}
}

func TestConvertToPS5RejectsWrongImageCount(t *testing.T) {
	if _, err := (Engine{}).ConvertToPS5(nil, nil, t.TempDir(), nil, nil); err == nil {
		t.Fatal("expected error for zero images")
	}
}

// TestConvertFromPS5RewritesPlatformOnly mirrors
// TestConvertToPS5RewritesPlatformOnly in the opposite direction
// (Prospero -> Steam). Uses the identical Build()-based mechanism; not
// itself field-tested in-game, unlike ConvertToPS5 - see docs/dev.md.
func TestConvertFromPS5RewritesPlatformOnly(t *testing.T) {
	ps5Data := syntheticBG3Archive(t, "Prospero")
	images := []gameapi.SaveImage{{Logical: "save", SaveName: "sdimg_Save0002", Payload: "AutoSave_0.lsv"}}
	result, err := (Engine{}).ConvertFromPS5(nil, images, map[string][]byte{"save": ps5Data}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, ok := result.Outputs["AutoSave_0.lsv"]
	if !ok {
		t.Fatalf("outputs = %#v, want key AutoSave_0.lsv", result.Outputs)
	}
	archive, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := archive.Find("SaveInfo.json")
	if !ok {
		t.Fatal("SaveInfo.json missing")
	}
	content, err := archive.ReadDecompressed(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte(`"Platform" : "Steam"`)) {
		t.Fatalf("SaveInfo.json = %s, want Platform rewritten to Steam", content)
	}
}

func TestConvertFromPS5DefaultsOutputNameWhenPayloadUnresolved(t *testing.T) {
	ps5Data := syntheticBG3Archive(t, "Prospero")
	images := []gameapi.SaveImage{{Logical: "save", SaveName: "sdimg_Save0002"}} // Payload left empty
	result, err := (Engine{}).ConvertFromPS5(nil, images, map[string][]byte{"save": ps5Data}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Outputs["sdimg_Save0002.lsv"]; !ok {
		t.Fatalf("outputs = %#v, want a sdimg_Save0002.lsv fallback key", result.Outputs)
	}
}

func TestInstallOutputsBacksUpExistingFileOnce(t *testing.T) {
	pcDir := t.TempDir()
	mustWriteFile(t, filepath.Join(pcDir, "MySave.lsv"), []byte("original"))
	backupDir := t.TempDir()

	err := (Engine{}).InstallOutputs(nil, map[string][]byte{"MySave.lsv": []byte("converted")}, pcDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, filepath.Join(backupDir, "MySave.lsv")); string(got) != "original" {
		t.Fatalf("backup = %q, want original content preserved", got)
	}
	if got := mustReadFile(t, filepath.Join(pcDir, "MySave.lsv")); string(got) != "converted" {
		t.Fatalf("installed = %q, want converted content", got)
	}
}

// TestInstallOutputsSkipsBackupWhenSourceMissing is the behavior that
// differs from unreal's InstallOutputs: installing a BG3 save that never
// existed in pcDir before (e.g. first-ever PS5->PC sync) is legitimate,
// so there's nothing to back up - InstallOutputs must not error just
// because the source file doesn't pre-exist.
func TestInstallOutputsSkipsBackupWhenSourceMissing(t *testing.T) {
	pcDir := t.TempDir()
	backupDir := t.TempDir()

	err := (Engine{}).InstallOutputs(nil, map[string][]byte{"NewSave.lsv": []byte("content")}, pcDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "NewSave.lsv")); !os.IsNotExist(err) {
		t.Fatal("expected no backup file to be created when source didn't exist")
	}
	if got := mustReadFile(t, filepath.Join(pcDir, "NewSave.lsv")); string(got) != "content" {
		t.Fatalf("installed = %q", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
