package reengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"savesyncpspc/internal/gameapi"
)

const prefix = "sdimg_SAVESERVICE-LINE-0-"

func testConfig(t *testing.T) Config {
	t.Helper()
	raw := json.RawMessage(`{
		"save_name_prefix": "` + prefix + `",
		"images": [{"logical":"save","label":"Save","dynamic_save_name":true,"dynamic_payload":true,"dynamic_pc_file":true}]
	}`)
	cfg, err := New().ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.(Config)
}

func TestParseConfigRequiresPrefixAndOneImage(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"no prefix", `{"images":[{"logical":"save"}]}`},
		{"no images", `{"save_name_prefix":"p_"}`},
		{"two images", `{"save_name_prefix":"p_","images":[{"logical":"a"},{"logical":"b"}]}`},
		{"image without logical", `{"save_name_prefix":"p_","images":[{"label":"x"}]}`},
	} {
		if _, err := New().ParseConfig(json.RawMessage(tc.raw)); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

// TestFileForSlot pins the slot-name -> filename mapping observed on
// real saves: the slot number is zero-padded to three digits, and the
// "Slot" suffix distinguishes a manual save from the autosave.
func TestFileForSlot(t *testing.T) {
	for _, tc := range []struct{ token, want string }{
		{"0", "data000.bin"},
		{"1Slot", "data001Slot.bin"},
		{"2Slot", "data002Slot.bin"},
		{"21Slot", "data021Slot.bin"},
	} {
		got, err := fileForSlot(tc.token)
		if err != nil {
			t.Errorf("%s: %v", tc.token, err)
			continue
		}
		if got != tc.want {
			t.Errorf("slot %q -> %q, want %q", tc.token, got, tc.want)
		}
	}
}

// TestFileForSlotRefusesGlobalProfile covers the file whose conversion
// was observed to crash the game at startup.
func TestFileForSlotRefusesGlobalProfile(t *testing.T) {
	_, err := fileForSlot(profileSlotToken)
	if err == nil {
		t.Fatal("expected the global profile slot to be refused")
	}
	if !strings.Contains(err.Error(), "crash") {
		t.Errorf("error should explain why it's refused, got: %v", err)
	}
}

func TestFileForSlotRejectsNonsense(t *testing.T) {
	for _, token := range []string{"", "abc", "Slot", "1.5"} {
		if _, err := fileForSlot(token); err == nil {
			t.Errorf("token %q: expected an error", token)
		}
	}
}

func TestSlotTokenRequiresPrefix(t *testing.T) {
	cfg := testConfig(t)
	if _, err := slotToken(cfg, "sdimg_SOMETHINGELSE-3"); err == nil {
		t.Error("expected an error for a save name with the wrong prefix")
	}
	got, err := slotToken(cfg, prefix+"21Slot")
	if err != nil {
		t.Fatal(err)
	}
	if got != "21Slot" {
		t.Errorf("got %q, want 21Slot", got)
	}
}

func TestResolvePayloadFindsTheSingleSaveFile(t *testing.T) {
	files := []string{"sce_sys", "sce_sys/param.sfo", "sce_sys/keystone", "sce_sys/icon0.png", "data001Slot.bin"}
	got, err := New().ResolvePayload(nil, gameapi.SaveImage{Logical: "save"}, files)
	if err != nil {
		t.Fatal(err)
	}
	if got != "data001Slot.bin" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePayloadRejectsAmbiguousContainer(t *testing.T) {
	for _, files := range [][]string{
		{"sce_sys/param.sfo"},
		{"data000.bin", "data001Slot.bin"},
	} {
		if _, err := New().ResolvePayload(nil, gameapi.SaveImage{Logical: "save"}, files); err == nil {
			t.Errorf("%v: expected an error", files)
		}
	}
}

// TestResolvePCFileDerivesFromTheSlot is the safety property that
// matters most here: a PC save directory holds every slot's file, and
// only the one matching the target slot may be chosen.
func TestResolvePCFileDerivesFromTheSlot(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"data000.bin", "data001Slot.bin", "data021Slot.bin"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig(t)
	for _, tc := range []struct{ saveName, want string }{
		{prefix + "0", "data000.bin"},
		{prefix + "1Slot", "data001Slot.bin"},
		{prefix + "21Slot", "data021Slot.bin"},
	} {
		got, err := New().ResolvePCFile(cfg, gameapi.SaveImage{Logical: "save", SaveName: tc.saveName}, dir)
		if err != nil {
			t.Errorf("%s: %v", tc.saveName, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s -> %q, want %q", tc.saveName, got, tc.want)
		}
	}
}

func TestResolvePCFileErrorsWhenSlotFileMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data000.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	_, err := New().ResolvePCFile(cfg, gameapi.SaveImage{Logical: "save", SaveName: prefix + "1Slot"}, dir)
	if err == nil {
		t.Fatal("expected an error when the slot's file isn't present")
	}
	if !strings.Contains(err.Error(), "data001Slot.bin") {
		t.Errorf("error should name the file it needed, got: %v", err)
	}
}

func TestResolvePCFileNeedsSaveNameFirst(t *testing.T) {
	cfg := testConfig(t)
	_, err := New().ResolvePCFile(cfg, gameapi.SaveImage{Logical: "save"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "ps5-save-name") {
		t.Fatalf("expected the error to point at --ps5-save-name, got %v", err)
	}
}

func TestExpectedSlotID(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  int32
	}{{"0", 0}, {"1Slot", 1}, {"21Slot", 21}, {profileSlotToken, -1}} {
		got, ok := expectedSlotID(tc.token)
		if !ok || got != tc.want {
			t.Errorf("token %q -> (%d, %v), want %d", tc.token, got, ok, tc.want)
		}
	}
}

func TestImagesCarriesDynamicFlags(t *testing.T) {
	imgs := New().Images(testConfig(t))
	if len(imgs) != 1 {
		t.Fatalf("got %d images", len(imgs))
	}
	if !imgs[0].DynamicSaveName || !imgs[0].DynamicPayload || !imgs[0].DynamicPCFile {
		t.Errorf("all three Dynamic* flags should be set, got %+v", imgs[0])
	}
}

func TestInspectRejectsNonDSSSPayload(t *testing.T) {
	v := New().Inspect(nil, "save", []byte("not a save at all"), 0, nil)
	if v.Portable {
		t.Error("garbage should not be portable")
	}
	if v.Tier == 0 {
		t.Error("expected a failing tier")
	}
}
