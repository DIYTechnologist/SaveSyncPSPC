package unreal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gvas"
)

// cleanTail is the None\0 property-map terminator the gate's tail check
// looks for. Test payloads that should pass the gate need to end with
// this; payloads built to exercise the tail check itself omit it.
const cleanTail = "None\x00"

// clairLikeConfig exercises the same shape games/clair.json's
// engine_config uses (module + versioned gameplay class + a stable
// container class), standing in for the deleted internal/games/clair test
// suite. It intentionally still uses a configured Module and full
// /Script/-style class paths so tests here can exercise checkModule and
// exact-match class comparison; the real shipped games/clair.json instead
// leaves Module empty and stores bare class-name suffixes, because real
// Clair saves use Blueprint (/Game/...) classes without a reliable module
// signal - see TestGateMatchesRealBlueprintContentPathsBySuffix for that
// shape specifically.
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
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"+cleanTail), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, _ := gvas.Parse(result.Outputs["EXPEDITION_0.sav"], "main")
	if got := result.Outputs["EXPEDITION_0.sav"][mainInfo.PropertiesOffset:]; string(got) != "ps5-main"+cleanTail {
		t.Fatalf("main payload = %q", got)
	}
	containerInfo, _ := gvas.Parse(result.Outputs["SavesContainer.sav"], "container")
	if got := result.Outputs["SavesContainer.sav"][containerInfo.PropertiesOffset:]; string(got) != "ps5-menu"+cleanTail {
		t.Fatalf("container payload = %q", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestConvertToPS5ConvertsBothPayloads(t *testing.T) {
	cfg := clairLikeConfig()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"+cleanTail), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))

	result, err := (Engine{}).ConvertToPS5(cfg, pcDir, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mainInfo, _ := gvas.Parse(result.Outputs["sdimg_EXPEDITION0"], "main")
	if got := result.Outputs["sdimg_EXPEDITION0"][mainInfo.PropertiesOffset:]; string(got) != "pc-main"+cleanTail {
		t.Fatalf("main payload = %q", got)
	}
	containerInfo, _ := gvas.Parse(result.Outputs["sdimg_SavesContainer"], "container")
	if got := result.Outputs["sdimg_SavesContainer"][containerInfo.PropertiesOffset:]; string(got) != "pc-menu"+cleanTail {
		t.Fatalf("container payload = %q", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
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

// --- Portability gate table tests ---
//
// These exercise the checks added for Phase 2. Each synthesizes a payload
// that trips exactly one check, using otherwise-clean fixtures (matching
// module, matching class, clean tail) so a false positive on another
// check can't produce a misleading pass/fail.

func gateTestConfig() Config {
	return clairLikeConfig()
}

func convertGameplayOnly(t *testing.T, cfg Config, ps5Payload, pcPayload []byte, overrides map[string]bool) error {
	t.Helper()
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), pcPayload)
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))
	_, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  ps5Payload,
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, overrides)
	return err
}

func TestGateBlocksEmbeddedSteamID64(t *testing.T) {
	cfg := gateTestConfig()
	// High dword 0x01100001 embedded in the property blob, little-endian.
	steamID := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x10, 0x01}
	payload := append(append([]byte("pc-main"), steamID...), []byte(cleanTail)...)
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", payload, 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, nil)
	if err == nil {
		t.Fatal("expected block for embedded SteamID64")
	}
	if !strings.Contains(err.Error(), CheckAccountID) {
		t.Fatalf("error = %v, want it to name %s", err, CheckAccountID)
	}
}

func TestGateBlocksAccountHintProperty(t *testing.T) {
	cfg := gateTestConfig()
	prop := testFString("SteamAccountID")
	payload := append(append([]byte{}, prop...), []byte(cleanTail)...)
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", payload, 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, nil)
	if err == nil {
		t.Fatal("expected block for account-hint property name")
	}
	if !strings.Contains(err.Error(), CheckAccountProps) {
		t.Fatalf("error = %v, want it to name %s", err, CheckAccountProps)
	}
}

func TestGateBlocksMissingCleanTail(t *testing.T) {
	cfg := gateTestConfig()
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main-no-terminator"), 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, nil)
	if err == nil {
		t.Fatal("expected block for missing clean tail")
	}
	if !strings.Contains(err.Error(), CheckTail) {
		t.Fatalf("error = %v, want it to name %s", err, CheckTail)
	}
}

func TestGateBlocksMismatchedModule(t *testing.T) {
	cfg := gateTestConfig()
	pcData := syntheticGVAS("/Script/OtherGame.BP_SaveGameObject_V8_C", []byte("pc-main"+cleanTail), 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, nil)
	if err == nil {
		t.Fatal("expected block for module mismatch")
	}
	if !strings.Contains(err.Error(), CheckModule) {
		t.Fatalf("error = %v, want it to name %s", err, CheckModule)
	}
}

func TestGateBlocksUnmappedClassPair(t *testing.T) {
	cfg := gateTestConfig()
	// V9 has no class_equivalence row and isn't identical to the PS5 side.
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V9_C", []byte("pc-main"+cleanTail), 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, nil)
	if err == nil {
		t.Fatal("expected block for unmapped class pair")
	}
	if !strings.Contains(err.Error(), CheckClassMap) {
		t.Fatalf("error = %v, want it to name %s", err, CheckClassMap)
	}
}

func TestGateIdentityClassMatchPassesWithoutRow(t *testing.T) {
	cfg := gateTestConfig()
	// Same class on both sides for the "container" logical, which has no
	// class_equivalence row at all - an implicit match, never blocked.
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-main"+cleanTail), 522))
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522),
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

// TestGateMatchesRealBlueprintContentPathsBySuffix mirrors what a real
// Clair save actually looks like: full Blueprint content paths under
// /Game/... that don't share a prefix with each other, let alone with a
// /Script/Module.Class-style row. Confirmed live against a real PS5 save;
// class_equivalence rows are matched by class-name suffix specifically so
// this works without needing to know or configure the content path.
func TestGateMatchesRealBlueprintContentPathsBySuffix(t *testing.T) {
	cfg := gateTestConfig()
	cfg.Module = "" // no reliable module signal for /Game/ Blueprint classes
	// Rows store bare suffixes here, exactly as games/clair.json actually
	// ships them - clairLikeConfig's full-path rows exist to exercise
	// checkModule/exact-match elsewhere and aren't what real profiles use.
	cfg.ClassEquivalence = []ClassEquivalence{
		{Logical: "gameplay", PC: "BP_SaveGameObject_V8_C", PS5: "BP_SaveGameObject_V7_C", Verified: true},
	}
	pcData := syntheticGVAS("/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V8.BP_SaveGameObject_V8_C", []byte("pc-main"+cleanTail), 522)
	ps5Data := syntheticGVAS("/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V7.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), pcData)
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Game/jRPGTemplate/Blueprints/SaveObjects/BP_jRPG_SavesContainer.BP_jRPG_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  ps5Data,
		"container": syntheticGVAS("/Game/jRPGTemplate/Blueprints/SaveObjects/BP_jRPG_SavesContainer.BP_jRPG_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, nil)
	if err != nil {
		t.Fatalf("real-shaped Blueprint class paths should match by suffix, got: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestGateCandidateClassPairWarnsWithoutBlocking(t *testing.T) {
	cfg := gateTestConfig()
	cfg.ClassEquivalence = append(cfg.ClassEquivalence, ClassEquivalence{
		Logical:  "gameplay-candidate",
		PC:       "/Script/Sandfall.BP_SaveGameObject_V9_C",
		PS5:      "/Script/Sandfall.BP_SaveGameObject_V7_C",
		Verified: false,
	})
	// Point "gameplay" itself at the candidate row instead of the verified one.
	cfg.ClassEquivalence[0] = ClassEquivalence{
		Logical:  "gameplay",
		PC:       "/Script/Sandfall.BP_SaveGameObject_V9_C",
		PS5:      "/Script/Sandfall.BP_SaveGameObject_V7_C",
		Verified: false,
	}
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V9_C", []byte("pc-main"+cleanTail), 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), pcData)
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))

	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  ps5Data,
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, nil)
	if err != nil {
		t.Fatalf("candidate row should proceed without --allow, got error: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly 1 (unverified candidate)", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "CANDIDATE") {
		t.Fatalf("warning = %q, want it flagged as a CANDIDATE", result.Warnings[0])
	}
}

func TestGateAllowOverridesOnlyTheNamedCheck(t *testing.T) {
	cfg := gateTestConfig()
	// Both an unmapped class pair AND an embedded SteamID64. Only
	// class-map is allowed, so the account-id block must still fire.
	steamID := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x10, 0x01}
	payload := append(append([]byte("pc-main"), steamID...), []byte(cleanTail)...)
	pcData := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V9_C", payload, 522)
	ps5Data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-main"+cleanTail), 522)

	err := convertGameplayOnly(t, cfg, ps5Data, pcData, map[string]bool{CheckClassMap: true})
	if err == nil {
		t.Fatal("expected account-id to still block despite class-map being allowed")
	}
	if strings.Contains(err.Error(), CheckClassMap) {
		t.Fatalf("error = %v, class-map should have been bypassed, not reported", err)
	}
	if !strings.Contains(err.Error(), CheckAccountID) {
		t.Fatalf("error = %v, want it to name %s", err, CheckAccountID)
	}

	// Now allow both - should proceed, with both surfaced as overridden.
	pcDir := t.TempDir()
	mustWrite(t, filepath.Join(pcDir, "EXPEDITION_0.sav"), pcData)
	mustWrite(t, filepath.Join(pcDir, "SavesContainer.sav"), syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("pc-menu"+cleanTail), 522))
	result, err := (Engine{}).ConvertFromPS5(cfg, map[string][]byte{
		"gameplay":  ps5Data,
		"container": syntheticGVAS("/Script/Sandfall.BP_SavesContainer_C", []byte("ps5-menu"+cleanTail), 522),
	}, pcDir, map[string]bool{CheckClassMap: true, CheckAccountID: true})
	if err != nil {
		t.Fatalf("expected success once both checks are allowed, got: %v", err)
	}
	if len(result.OverriddenChecks) != 2 {
		t.Fatalf("overridden checks = %#v, want class-map and account-id", result.OverriddenChecks)
	}
}

func TestInspectSinglePayloadReportsBlockedChecks(t *testing.T) {
	cfg := gateTestConfig()
	payload := syntheticGVAS("/Script/OtherGame.BP_SaveGameObject_V8_C", []byte("pc-main"), 522)
	verdict := (Engine{}).Inspect(cfg, "gameplay", payload, engine.SidePC, nil)
	if verdict.Portable {
		t.Fatal("expected not portable: wrong module and missing tail")
	}
	var sawModule, sawTail bool
	for _, c := range verdict.Checks {
		if c.Check == CheckModule && !c.Passed {
			sawModule = true
		}
		if c.Check == CheckTail && !c.Passed {
			sawTail = true
		}
	}
	if !sawModule || !sawTail {
		t.Fatalf("checks = %#v, want both module and tail to fail", verdict.Checks)
	}
}

func TestInspectMagicFailureIsTierThreeAndNotOverridable(t *testing.T) {
	verdict := (Engine{}).Inspect(gateTestConfig(), "gameplay", []byte("not a save file"), engine.SidePC, map[string]bool{"magic": true})
	if verdict.Portable {
		t.Fatal("expected tier-3 failure to never be portable, override or not")
	}
	if verdict.Tier != engine.TierWrongFormat {
		t.Fatalf("tier = %v, want TierWrongFormat", verdict.Tier)
	}
}
