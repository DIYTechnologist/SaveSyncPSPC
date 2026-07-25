package unreal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gvas"
)

// Override tokens for --allow. Each names exactly one tier-2 check; there
// is deliberately no token for a tier-3 (CheckMagic) failure.
const (
	CheckMagic          = "magic"
	CheckModule         = "module"
	CheckAccountID      = "account-id"
	CheckAccountProps   = "account-props"
	CheckTail           = "tail"
	CheckPackageVersion = "package-version"
	CheckClassMap       = "class-map"
)

// AllowTokens lists every override token a caller (the CLI's --allow
// flag) may name. Used to reject unknown tokens with the valid list.
var AllowTokens = []string{CheckModule, CheckAccountID, CheckAccountProps, CheckTail, CheckPackageVersion, CheckClassMap}

// steamID64HighDword is the high 32 bits of a SteamID64 for an individual
// account (universe=1 "public", account type=1 "individual"). A payload
// containing any 8-byte little-endian run with this high dword almost
// certainly embeds a real Steam account identifier.
const steamID64HighDword = 0x01100001

var accountHints = []string{"steam", "psn", "account", "uniquenetid", "userid", "epicid"}

// Inspect runs the portability gate against one payload for one logical
// image. It never writes anything and is used both by `save-sync inspect`
// and internally before any graft.
func (Engine) Inspect(cfgAny any, logical string, payload []byte, side engine.Side, overrides map[string]bool) engine.Verdict {
	cfg, _ := cfgAny.(Config)
	info, err := gvas.Parse(payload, logical)
	if err != nil {
		return verdictFromChecks([]engine.CheckResult{{
			Logical: logical,
			Check:   CheckMagic,
			Tier:    engine.TierWrongFormat,
			Passed:  false,
			Reason:  err.Error(),
		}})
	}
	checks := []engine.CheckResult{
		{Check: CheckMagic, Tier: engine.TierWrongFormat, Passed: true},
		checkModule(cfg, info.SaveClass, overrides),
		checkAccountID(payload, overrides),
		checkAccountProps(payload, overrides),
		checkTail(payload, overrides),
	}
	for i := range checks {
		checks[i].Logical = logical
	}
	return verdictFromChecks(checks)
}

func verdictFromChecks(checks []engine.CheckResult) engine.Verdict {
	verdict := engine.Verdict{Portable: true, Checks: checks}
	for _, c := range checks {
		if !c.Passed && !c.Overridden {
			verdict.Portable = false
			if c.Tier > verdict.Tier {
				verdict.Tier = c.Tier
			}
		}
	}
	return verdict
}

func moduleFromClass(class string) string {
	const prefix = "/Script/"
	if !strings.HasPrefix(class, prefix) {
		return ""
	}
	rest := class[len(prefix):]
	if idx := strings.Index(rest, "."); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

func checkModule(cfg Config, class string, overrides map[string]bool) engine.CheckResult {
	if cfg.Module == "" {
		return engine.CheckResult{Check: CheckModule, Tier: engine.TierBlocked, Passed: true}
	}
	actual := moduleFromClass(class)
	if actual == cfg.Module {
		return engine.CheckResult{Check: CheckModule, Tier: engine.TierBlocked, Passed: true}
	}
	reason := fmt.Sprintf("expected Unreal module %q, got %q from class %s - looks like a different game", cfg.Module, actual, class)
	return engine.CheckResult{Check: CheckModule, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckModule], Reason: reason}
}

// hasEmbeddedSteamID64 scans every byte offset (not just 8-byte aligned
// ones - property blobs have no fixed alignment) for a little-endian
// uint64 whose high dword matches a real Steam account identifier.
func hasEmbeddedSteamID64(payload []byte) bool {
	for i := 0; i+8 <= len(payload); i++ {
		if binary.LittleEndian.Uint64(payload[i:i+8])>>32 == steamID64HighDword {
			return true
		}
	}
	return false
}

func checkAccountID(payload []byte, overrides map[string]bool) engine.CheckResult {
	if !hasEmbeddedSteamID64(payload) {
		return engine.CheckResult{Check: CheckAccountID, Tier: engine.TierBlocked, Passed: true}
	}
	reason := "found an embedded SteamID64 - this save is account-bound"
	return engine.CheckResult{Check: CheckAccountID, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckAccountID], Reason: reason}
}

// findAccountHintProperty scans for a structurally valid FString - a
// length-prefixed (int32) run whose length matches the actual run plus a
// null terminator - so a raw substring match inside compressed or binary
// regions doesn't produce false positives. Only a property name (short,
// printable ASCII) that contains one of accountHints counts.
func findAccountHintProperty(payload []byte) string {
	for i := 0; i+5 <= len(payload); i++ {
		length := int32(binary.LittleEndian.Uint32(payload[i : i+4]))
		if length <= 0 || length > 256 {
			continue
		}
		end := i + 4 + int(length)
		if end > len(payload) || payload[end-1] != 0 {
			continue
		}
		raw := payload[i+4 : end-1]
		if !isPrintableASCII(raw) {
			continue
		}
		lower := strings.ToLower(string(raw))
		for _, hint := range accountHints {
			if strings.Contains(lower, hint) {
				return string(raw)
			}
		}
	}
	return ""
}

func isPrintableASCII(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func checkAccountProps(payload []byte, overrides map[string]bool) engine.CheckResult {
	if prop := findAccountHintProperty(payload); prop != "" {
		reason := fmt.Sprintf("found a property name suggesting account binding: %q", prop)
		return engine.CheckResult{Check: CheckAccountProps, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckAccountProps], Reason: reason}
	}
	return engine.CheckResult{Check: CheckAccountProps, Tier: engine.TierBlocked, Passed: true}
}

// hasCleanTail reports whether the final bytes of payload contain the
// None\0 property-map terminator Unreal writes at the end of a property
// list. Its absence suggests a trailer or checksum this tool can't
// reproduce. This is a byte-scan heuristic, not a structural parse.
func hasCleanTail(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	start := len(payload) - 32
	if start < 0 {
		start = 0
	}
	return bytes.Contains(payload[start:], []byte("None\x00"))
}

func checkTail(payload []byte, overrides map[string]bool) engine.CheckResult {
	if hasCleanTail(payload) {
		return engine.CheckResult{Check: CheckTail, Tier: engine.TierBlocked, Passed: true}
	}
	reason := `no "None\0" property-map terminator found in the final 32 bytes; a trailer or checksum may not be reproducible`
	return engine.CheckResult{Check: CheckTail, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckTail], Reason: reason}
}

func packageUE5(info gvas.Info) uint32 {
	if info.PackageVersionUE5 != nil {
		return *info.PackageVersionUE5
	}
	return 0
}

func packageVersionsMismatch(source, target gvas.Info) bool {
	if source.PackageVersionUE4 != target.PackageVersionUE4 {
		return true
	}
	return (source.PackageVersionUE5 == nil) != (target.PackageVersionUE5 == nil) || packageUE5(source) != packageUE5(target)
}

func checkPackageVersion(cfg Config, source, target gvas.Info, overrides map[string]bool) engine.CheckResult {
	if !packageVersionsMismatch(source, target) {
		return engine.CheckResult{Check: CheckPackageVersion, Tier: engine.TierBlocked, Passed: true}
	}
	reason := fmt.Sprintf("package versions differ: UE4 %d != %d, UE5 %v != %v", source.PackageVersionUE4, target.PackageVersionUE4, source.PackageVersionUE5, target.PackageVersionUE5)
	overridden := overrides[CheckPackageVersion] || cfg.AllowPackageVersionMismatch
	return engine.CheckResult{Check: CheckPackageVersion, Tier: engine.TierBlocked, Passed: false, Overridden: overridden, Reason: reason}
}

// checkClassMap enforces the class_equivalence table: an identical class
// on both sides is always an implicit match; otherwise a row must name a
// (pc, ps5) pair matching this one. A verified:false candidate row
// satisfies the check without requiring --allow, but is still surfaced as
// a warning (Warn) on every run, per the three-state design in
// docs/dev.md (verified / candidate / unmapped).
//
// Row values match by suffix (strings.HasSuffix), not full-path equality.
// Real Unreal save classes for Blueprint SaveGame objects are full
// content paths (e.g.
// "/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V7.BP_SaveGameObject_V7_C"),
// and the content folder isn't guaranteed stable or even consistent
// between logical images of the same game - the validated, working
// signal (carried over from this tool's original hardcoded
// GameplayClassSuffix check) is just the trailing class name.
func checkClassMap(cfg Config, logical string, source, target gvas.Info, direction string, overrides map[string]bool) engine.CheckResult {
	if source.SaveClass == target.SaveClass {
		return engine.CheckResult{Check: CheckClassMap, Tier: engine.TierBlocked, Passed: true}
	}
	var row ClassEquivalence
	found := false
	for _, r := range cfg.ClassEquivalence {
		if r.Logical == logical {
			row, found = r, true
			break
		}
	}
	if !found {
		reason := fmt.Sprintf("unmapped class pair for %s: %s != %s; add a class_equivalence row (see `save-sync inspect --record`)", logical, source.SaveClass, target.SaveClass)
		return engine.CheckResult{Check: CheckClassMap, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckClassMap], Reason: reason}
	}
	sourceExpected, targetExpected := row.PC, row.PS5
	if direction == "ps5-to-pc" {
		sourceExpected, targetExpected = row.PS5, row.PC
	}
	if !strings.HasSuffix(source.SaveClass, sourceExpected) || !strings.HasSuffix(target.SaveClass, targetExpected) {
		reason := fmt.Sprintf("class pair for %s doesn't match the configured row: got %s / %s, row expects a suffix of %s / %s", logical, source.SaveClass, target.SaveClass, sourceExpected, targetExpected)
		return engine.CheckResult{Check: CheckClassMap, Tier: engine.TierBlocked, Passed: false, Overridden: overrides[CheckClassMap], Reason: reason}
	}
	if !row.Verified {
		reason := fmt.Sprintf("candidate class mapping for %s not yet verified: %s <-> %s", logical, row.PC, row.PS5)
		return engine.CheckResult{Check: CheckClassMap, Tier: engine.TierBlocked, Passed: true, Warn: true, Reason: reason}
	}
	return engine.CheckResult{Check: CheckClassMap, Tier: engine.TierBlocked, Passed: true}
}
