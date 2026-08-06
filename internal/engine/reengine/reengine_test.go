package reengine

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"savesyncpspc/internal/gameapi"
	re "savesyncpspc/internal/reengine"
)

const prefix = "sdimg_SAVESERVICE-LINE-0-"

func testConfig(t *testing.T) Config {
	t.Helper()
	raw := json.RawMessage(`{
		"title": "re2",
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
		{"no prefix", `{"title":"re2","images":[{"logical":"save"}]}`},
		{"no title", `{"save_name_prefix":"p_","images":[{"logical":"save"}]}`},
		{"unknown title", `{"title":"re999","save_name_prefix":"p_","images":[{"logical":"save"}]}`},
		{"no images", `{"title":"re2","save_name_prefix":"p_"}`},
		{"two images", `{"title":"re2","save_name_prefix":"p_","images":[{"logical":"a"},{"logical":"b"}]}`},
		{"image without logical", `{"title":"re2","save_name_prefix":"p_","images":[{"label":"x"}]}`},
	} {
		if _, err := New().ParseConfig(json.RawMessage(tc.raw)); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

func TestParseConfigAcceptsRE3(t *testing.T) {
	raw := json.RawMessage(`{
		"title": "re3",
		"save_name_prefix": "` + prefix + `",
		"images": [{"logical":"save","label":"Save","dynamic_save_name":true,"dynamic_payload":true,"dynamic_pc_file":true}]
	}`)
	if _, err := New().ParseConfig(raw); err != nil {
		t.Fatalf("expected re3 to be a valid title, got: %v", err)
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
	v := New().Inspect(testConfig(t), "save", []byte("not a save at all"), 0, nil)
	if v.Portable {
		t.Error("garbage should not be portable")
	}
	if v.Tier == 0 {
		t.Error("expected a failing tier")
	}
}

func re3TestConfig(t *testing.T) Config {
	t.Helper()
	raw := json.RawMessage(`{
		"title": "re3",
		"save_name_prefix": "` + prefix + `",
		"images": [{"logical":"save","label":"Save","dynamic_save_name":true,"dynamic_payload":true,"dynamic_pc_file":true}]
	}`)
	cfg, err := New().ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.(Config)
}

// TestConvertFromPS5RequiresSteamIDForRE3 is a regression test: RE2/RE3
// PC saves embed the target account's SteamID64 (see docs/dev-res2.md),
// but ConvertFromPS5 used to hardcode 0 there instead of forwarding
// image.SteamID - meaning every PS5->PC conversion silently produced a
// save the real game would very likely refuse to load, with no signal
// to the caller that anything was wrong. It must now fail clearly when
// no --steam-id was given, the same way RE4's equivalent path already
// does (see TestConvertRE4FromPS5RequiresSteamID) - checked before any
// data is even touched, so garbage payload bytes are fine here.
func TestConvertFromPS5RequiresSteamIDForRE3(t *testing.T) {
	cfg := re3TestConfig(t)
	image := gameapi.SaveImage{Logical: "save", SaveName: prefix + "0"}
	ps5Payloads := map[string][]byte{"save": []byte("irrelevant, should fail before reading this")}
	_, err := New().ConvertFromPS5(cfg, []gameapi.SaveImage{image}, ps5Payloads, "", nil)
	if err == nil {
		t.Fatal("expected an error when SteamID is unset")
	}
}

// buildRE3PlatformBody constructs the minimal RSZ body ConvertPCToPS5's
// platform-field patch and checkSlot's trailing slot-id read both need:
// one object of RE3's platform class (re.RE3.PlatformClass) carrying the
// PC-shaped platform enum/bool fields (see internal/reengine/convert.go
// fieldPlatformEnum/fieldPlatformBool), followed by the 4-byte
// little-endian slot id every save carries at the very end of its body.
// Mirrors internal/reengine's own (unexported) buildPlatformBodyFor test
// helper, rebuilt here by hand since it isn't exported across packages.
func buildRE3PlatformBody(slotID int32) []byte {
	var b bytes.Buffer
	u32 := func(v uint32) {
		var raw [4]byte
		binary.LittleEndian.PutUint32(raw[:], v)
		b.Write(raw[:])
	}
	u32(0xAAAAAAAA)                      // outer object hash - not itself validated
	u32(2)                               // field count
	u32(re.RE3.PlatformClass)            // class hash
	const fieldPlatformEnum = 0xb41fa365 // PC = 3, PS5 = 2 - see convert.go
	const fieldPlatformBool = 0xe231b945 // PC = true, PS5 = false - see convert.go
	u32(fieldPlatformEnum)
	u32(uint32(re.FieldTypeEnum))
	u32(4) // declared size
	u32(3) // enum value: PC
	u32(fieldPlatformBool)
	u32(uint32(re.FieldTypeBoolean))
	u32(1)                   // declared size
	b.WriteByte(1)           // bool value: true (PC)
	b.Write([]byte{0, 0, 0}) // readField's post-value alignUp(4)
	u32(uint32(slotID))
	return b.Bytes()
}

// TestConvertFromPS5ForwardsSteamIDForRE3 is the positive counterpart of
// TestConvertFromPS5RequiresSteamIDForRE3: given a real --steam-id, the
// resulting PC save must actually embed it, not silently fall back to 0.
// The value here is a 32-bit account id, the form real PC saves carry
// (bridge.steamAccountID normalizes a SteamID64 down to this before the
// engine ever sees it - see TestSteamAccountIDNormalizesSteamID64).
func TestConvertFromPS5ForwardsSteamIDForRE3(t *testing.T) {
	cfg := re3TestConfig(t)
	const wantSteamID = uint64(11052978)

	pcBody := buildRE3PlatformBody(0)
	pcData, err := re.Build(pcBody, re.RE3.Key, re.BuildOptions{HasID: true, SteamID: 12345})
	if err != nil {
		t.Fatal(err)
	}
	ps5Data, err := re.RE3.ConvertPCToPS5(pcData)
	if err != nil {
		t.Fatal(err)
	}

	image := gameapi.SaveImage{Logical: "save", SaveName: prefix + "0", SteamID: wantSteamID}
	result, err := New().ConvertFromPS5(cfg, []gameapi.SaveImage{image}, map[string][]byte{"save": ps5Data}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	converted, ok := result.Outputs["data000.bin"]
	if !ok {
		t.Fatalf("outputs = %#v, want data000.bin", result.Outputs)
	}
	dec, err := re.Decode(converted, re.RE3.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.HasID {
		t.Error("expected the converted PC save to carry an ID field")
	}
	if dec.SteamID != wantSteamID {
		t.Errorf("embedded SteamID = %d, want %d", dec.SteamID, wantSteamID)
	}
}
