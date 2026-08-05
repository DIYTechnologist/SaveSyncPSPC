package unityblb

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"savesyncpspc/internal/engine"
)

func testConfig() Config {
	return Config{Images: []ImageConfig{{
		Logical:  "slot0",
		SaveName: "sdimg_slot0000",
		Label:    "Slot 1",
		PCDir:    "slot0000",
		Payload:  "slot0000.blb",
	}}}
}

func TestParseConfigRejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"images":[]}`,
		`{"images":[{"save_name":"sdimg_slot0000","pc_dir":"slot0000","payload":"slot0000.blb"}]}`,
		`{"images":[{"logical":"slot0","pc_dir":"slot0000","payload":"slot0000.blb"}]}`,
		`{"images":[{"logical":"slot0","save_name":"sdimg_slot0000","payload":"slot0000.blb"}]}`,
		`{"images":[{"logical":"slot0","save_name":"sdimg_slot0000","pc_dir":"slot0000"}]}`,
	}
	for _, c := range cases {
		if _, err := (Engine{}).ParseConfig(json.RawMessage(c)); err == nil {
			t.Errorf("expected an error for config %s", c)
		}
	}
}

func TestParseConfigAcceptsValidConfig(t *testing.T) {
	raw, err := json.Marshal(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Engine{}).ParseConfig(raw); err != nil {
		t.Fatalf("expected a valid config to parse, got: %v", err)
	}
}

func TestImagesMapsPCDirToPCFile(t *testing.T) {
	images := (Engine{}).Images(testConfig())
	if len(images) != 1 {
		t.Fatalf("got %d images, want 1", len(images))
	}
	img := images[0]
	if img.PCFile != "slot0000" {
		t.Fatalf("PCFile = %q, want %q", img.PCFile, "slot0000")
	}
	if img.SaveName != "sdimg_slot0000" || img.Payload != "slot0000.blb" {
		t.Fatalf("unexpected image: %+v", img)
	}
	if img.DynamicSaveName || img.DynamicPayload || img.DynamicPCFile {
		t.Fatal("unityblb images should never be dynamic")
	}
}

func TestCompatibility(t *testing.T) {
	c := (Engine{}).Compatibility(testConfig())
	if !c.Convertible {
		t.Fatal("expected Convertible=true")
	}
	if c.PC.Platform != "Steam" || c.PS5.Platform != "PS5" {
		t.Fatalf("unexpected platforms: %+v", c)
	}
}

func TestOverrideTokens(t *testing.T) {
	tokens := (Engine{}).OverrideTokens()
	if len(tokens) != 1 || tokens[0] != CheckProtoVersion {
		t.Fatalf("got %v, want [%s]", tokens, CheckProtoVersion)
	}
}

func TestInspectPS5Side(t *testing.T) {
	valid, err := Encode([]Entry{{Name: "gameinfo.json", Data: []byte(`{"protoBufVersion":13}`)}})
	if err != nil {
		t.Fatal(err)
	}
	verdict := (Engine{}).Inspect(testConfig(), "slot0", valid, engine.SidePS5, nil)
	if !verdict.Portable {
		t.Fatalf("expected valid container to be portable, got %+v", verdict)
	}

	verdict = (Engine{}).Inspect(testConfig(), "slot0", []byte("not a container"), engine.SidePS5, nil)
	if verdict.Portable || verdict.Tier != engine.TierWrongFormat {
		t.Fatalf("expected a wrong-format verdict for garbage bytes, got %+v", verdict)
	}
}

func TestInspectPCSide(t *testing.T) {
	verdict := (Engine{}).Inspect(testConfig(), "slot0", []byte(`{"protoBufVersion":13}`), engine.SidePC, nil)
	if !verdict.Portable {
		t.Fatalf("expected valid gameinfo.json to be portable, got %+v", verdict)
	}

	verdict = (Engine{}).Inspect(testConfig(), "slot0", []byte(`{"version":2}`), engine.SidePC, nil)
	if verdict.Portable {
		t.Fatal("expected a missing protoBufVersion to fail")
	}

	verdict = (Engine{}).Inspect(testConfig(), "slot0", []byte(`not json`), engine.SidePC, nil)
	if verdict.Portable {
		t.Fatal("expected invalid JSON to fail")
	}
}

// TestInspectPCSideHandlesBOM covers a real gameinfo.json's leading
// UTF-8 byte-order mark (observed on both PC and PS5), which
// encoding/json otherwise rejects outright.
func TestInspectPCSideHandlesBOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"protoBufVersion":13}`)...)
	verdict := (Engine{}).Inspect(testConfig(), "slot0", withBOM, engine.SidePC, nil)
	if !verdict.Portable {
		t.Fatalf("expected a BOM-prefixed gameinfo.json to be portable, got %+v", verdict)
	}
}

// buildSyntheticPS5Blb builds a .blb matching a real save's shape: four
// loose files plus two CellsCache cells sharing batch id "5".
func buildSyntheticPS5Blb(t *testing.T, protoVersion int) []byte {
	t.Helper()
	entries := []Entry{
		{Name: "gameinfo.json", Data: append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"version":2,"protoBufVersion":`+strconv.Itoa(protoVersion)+`}`)...)},
		{Name: "screenshot.jpg", Data: []byte("jpeg-bytes")},
		{Name: "scene-objects.bin", Data: []byte("scene-bytes")},
		{Name: "global-objects.bin", Data: []byte("global-bytes")},
		{Name: "CellsCache/baked-batch-cells-5-1-1.bin", Data: []byte("cell-a")},
		{Name: "CellsCache/baked-batch-cells-5-1-2.bin", Data: []byte("cell-b")},
	}
	data, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConvertFromPS5WritesExpectedOutputs(t *testing.T) {
	cfg := testConfig()
	ps5Data := buildSyntheticPS5Blb(t, 3)
	pcDir := t.TempDir() // no pre-existing PC save

	result, err := (Engine{}).ConvertFromPS5(cfg, nil, map[string][]byte{"slot0": ps5Data}, pcDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"slot0000/gameinfo.json",
		"slot0000/screenshot.jpg",
		"slot0000/scene-objects.bin",
		"slot0000/global-objects.bin",
		"slot0000/CellsCache/baked-batch-cells-5-grp0.zip",
	}
	for _, w := range want {
		if _, ok := result.Outputs[w]; !ok {
			t.Errorf("missing expected output %q; got keys: %v", w, keysOf(result.Outputs))
		}
	}
	if len(result.Outputs) != len(want) {
		t.Fatalf("got %d outputs, want %d: %v", len(result.Outputs), len(want), keysOf(result.Outputs))
	}

	zipData := result.Outputs["slot0000/CellsCache/baked-batch-cells-5-grp0.zip"]
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("got %d files in regrouped zip, want 2", len(zr.File))
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestConvertFromPS5BlocksOnProtoVersionMismatch(t *testing.T) {
	cfg := testConfig()
	ps5Data := buildSyntheticPS5Blb(t, 3)

	pcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pcDir, "slot0000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pcDir, "slot0000", "gameinfo.json"), []byte(`{"protoBufVersion":9}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (Engine{}).ConvertFromPS5(cfg, nil, map[string][]byte{"slot0": ps5Data}, pcDir, nil)
	if err == nil {
		t.Fatal("expected a protoBufVersion mismatch to block the conversion")
	}

	result, err := (Engine{}).ConvertFromPS5(cfg, nil, map[string][]byte{"slot0": ps5Data}, pcDir, map[string]bool{CheckProtoVersion: true})
	if err != nil {
		t.Fatalf("expected override to unblock the conversion, got: %v", err)
	}
	if len(result.OverriddenChecks) != 1 || result.OverriddenChecks[0] != CheckProtoVersion {
		t.Fatalf("expected OverriddenChecks to report %s, got %v", CheckProtoVersion, result.OverriddenChecks)
	}
}

// buildSyntheticPCDir writes a PC-shaped slot directory: loose files plus
// one CellsCache zip (Stored) bundling two cells under batch id "5" - the
// same shape observed in a real Steam Subnautica save.
func buildSyntheticPCDir(t *testing.T, root string, protoVersion int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "CellsCache"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"gameinfo.json":      "\xEF\xBB\xBF" + `{"version":2,"protoBufVersion":` + strconv.Itoa(protoVersion) + `}`,
		"screenshot.jpg":     "jpeg-bytes",
		"scene-objects.bin":  "scene-bytes",
		"global-objects.bin": "global-bytes",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, cell := range []string{"baked-batch-cells-5-1-1.bin", "baked-batch-cells-5-1-2.bin"} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: cell, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("cell-content-" + cell))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CellsCache", "baked-batch-cells-5-grp0.zip"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConvertToPS5ProducesValidContainer(t *testing.T) {
	cfg := testConfig()
	pcDir := t.TempDir()
	buildSyntheticPCDir(t, filepath.Join(pcDir, "slot0000"), 3)

	result, err := (Engine{}).ConvertToPS5(cfg, nil, pcDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := result.Outputs["sdimg_slot0000"]
	if !ok {
		t.Fatalf("missing output for sdimg_slot0000; got %v", keysOf(result.Outputs))
	}
	entries, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 { // 4 loose + 2 flattened cells
		t.Fatalf("got %d entries, want 6", len(entries))
	}
	if _, ok := Find(entries, "CellsCache/baked-batch-cells-5-1-1.bin"); !ok {
		t.Fatal("expected flattened cell entry in the rebuilt container")
	}
}

func TestConvertToPS5MissingGameInfoErrors(t *testing.T) {
	cfg := testConfig()
	pcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pcDir, "slot0000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (Engine{}).ConvertToPS5(cfg, nil, pcDir, nil, nil); err == nil {
		t.Fatal("expected an error when gameinfo.json is missing")
	}
}

func TestConvertToPS5BlocksOnProtoVersionMismatch(t *testing.T) {
	cfg := testConfig()
	pcDir := t.TempDir()
	buildSyntheticPCDir(t, filepath.Join(pcDir, "slot0000"), 3)
	template := buildSyntheticPS5Blb(t, 9)

	_, err := (Engine{}).ConvertToPS5(cfg, nil, pcDir, map[string][]byte{"slot0": template}, nil)
	if err == nil {
		t.Fatal("expected a protoBufVersion mismatch against the PS5 template to block the conversion")
	}

	result, err := (Engine{}).ConvertToPS5(cfg, nil, pcDir, map[string][]byte{"slot0": template}, map[string]bool{CheckProtoVersion: true})
	if err != nil {
		t.Fatalf("expected override to unblock the conversion, got: %v", err)
	}
	if len(result.OverriddenChecks) != 1 {
		t.Fatalf("expected one overridden check, got %v", result.OverriddenChecks)
	}
}

func TestInstallOutputsFullReplaceRemovesStaleFiles(t *testing.T) {
	cfg := testConfig()
	pcDir := t.TempDir()
	slotDir := filepath.Join(pcDir, "slot0000")
	buildSyntheticPCDir(t, slotDir, 3)
	// A stale zip from a previous, differently-explored save that must
	// not survive the install.
	staleZip := filepath.Join(slotDir, "CellsCache", "baked-batch-cells-99-grp0.zip")
	if err := os.WriteFile(staleZip, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	outputs := map[string][]byte{
		"slot0000/gameinfo.json":                           []byte(`{"protoBufVersion":4}`),
		"slot0000/CellsCache/baked-batch-cells-7-grp0.zip": []byte("fresh-zip"),
	}
	if err := (Engine{}).InstallOutputs(cfg, outputs, pcDir, backupDir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(staleZip); !os.IsNotExist(err) {
		t.Fatal("expected the stale zip to be removed by a full-replace install")
	}
	if _, err := os.Stat(filepath.Join(slotDir, "CellsCache", "baked-batch-cells-7-grp0.zip")); err != nil {
		t.Fatal("expected the new zip to be written")
	}
	got, err := os.ReadFile(filepath.Join(slotDir, "gameinfo.json"))
	if err != nil || string(got) != `{"protoBufVersion":4}` {
		t.Fatalf("gameinfo.json = %q, err %v", got, err)
	}

	// The pre-existing directory (including the stale zip) must have been
	// backed up before removal.
	if _, err := os.Stat(filepath.Join(backupDir, "slot0000", "CellsCache", "baked-batch-cells-99-grp0.zip")); err != nil {
		t.Fatal("expected the original directory to be backed up before replacement")
	}
}
