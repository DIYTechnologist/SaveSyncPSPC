// Package unreal implements engine.Engine for Unreal Engine's GVAS save
// format. It replaces what used to be a per-game Go plugin (e.g.
// internal/games/clair) with one generic implementation driven entirely by
// a game profile's engine_config: which Garlic save images it needs, and
// which (PC class, PS5 class) pairs are known-compatible envelope grafts.
package unreal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/gvas"
	"savesyncpspc/internal/util"
)

// ImageConfig describes one Garlic save image this game needs.
type ImageConfig struct {
	Logical  string `json:"logical"`
	SaveName string `json:"save_name"`
	Label    string `json:"label"`
	PCFile   string `json:"pc_file"`
	Payload  string `json:"payload"`
}

// ClassEquivalence declares one known-compatible (PC class, PS5 class)
// pair for a logical image. PC/PS5 are matched against the actual save
// class by suffix (see checkClassMap in gate.go), not full-path equality:
// real Blueprint SaveGame classes are full content paths
// (e.g. "/Game/Gameplay/Save/SaveObjects/BP_SaveGameObject_V7.BP_SaveGameObject_V7_C"),
// and only the trailing class name ("BP_SaveGameObject_V7_C") has been a
// stable, validated signal in practice - the content path prefix is not
// guaranteed. See Config.ClassEquivalence.
type ClassEquivalence struct {
	Logical  string `json:"logical"`
	PC       string `json:"pc"`
	PS5      string `json:"ps5"`
	Verified bool   `json:"verified"`
	Note     string `json:"note"`
}

// Config is the engine_config block for a games/<key>.json profile using
// the "unreal" engine.
type Config struct {
	// Module is the Unreal module a save class's *native* (/Script/...)
	// path should belong to, e.g. "Sandfall" for
	// "/Script/Sandfall.BP_SaveGameObject_V8_C". Leave empty (disabling
	// the module-match check) for games whose SaveGame classes are
	// Blueprints under /Game/... instead: those paths carry no reliable,
	// consistent module-like signal across a game's different save
	// images (confirmed against Clair's real saves, which use two
	// entirely different /Game/ content folders for its two images).
	Module string `json:"module"`

	Images []ImageConfig `json:"images"`

	// ClassEquivalence lists which (pc, ps5) class pairs are known-good
	// envelope grafts for a given logical image. A pair with the same
	// class on both sides is always implicitly fine and needn't be
	// listed. A logical image with no row here isn't class-checked.
	ClassEquivalence []ClassEquivalence `json:"class_equivalence"`

	// AllowPackageVersionMismatch downgrades a UE4/UE5 package-version
	// mismatch from a hard error to a warning. Only set this once a
	// specific mismatch has been manually verified safe for a game/build.
	AllowPackageVersionMismatch bool `json:"allow_package_version_mismatch"`
}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Name() string { return "unreal" }

func (Engine) OverrideTokens() []string { return append([]string(nil), AllowTokens...) }

func (Engine) ParseConfig(raw json.RawMessage) (any, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid unreal engine_config: %w", err)
	}
	if len(cfg.Images) == 0 {
		return nil, fmt.Errorf("unreal engine_config has no images")
	}
	for _, img := range cfg.Images {
		if img.Logical == "" || img.SaveName == "" || img.PCFile == "" || img.Payload == "" {
			return nil, fmt.Errorf("unreal engine_config image entries require logical, save_name, pc_file, and payload")
		}
	}
	return cfg, nil
}

func (Engine) Images(cfgAny any) []gameapi.SaveImage {
	cfg := cfgAny.(Config)
	out := make([]gameapi.SaveImage, len(cfg.Images))
	for i, img := range cfg.Images {
		out[i] = gameapi.SaveImage{
			Logical:  img.Logical,
			SaveName: img.SaveName,
			Label:    img.Label,
			PCFile:   img.PCFile,
			Payload:  img.Payload,
		}
	}
	return out
}

var versionSuffix = regexp.MustCompile(`_V(\d+)_`)

// Compatibility derives a display-friendly PC<->PS5 summary from the
// "gameplay" logical's class_equivalence row, if any.
func (Engine) Compatibility(cfgAny any) gameapi.Compatibility {
	cfg := cfgAny.(Config)
	for _, row := range cfg.ClassEquivalence {
		if row.Logical != "gameplay" {
			continue
		}
		return gameapi.Compatibility{
			PC:          gameapi.CompatibilitySide{Platform: "Steam", GameplayClassSuffix: ClassSuffix(row.PC), Version: classVersion(row.PC)},
			PS5:         gameapi.CompatibilitySide{Platform: "PS5", GameplayClassSuffix: ClassSuffix(row.PS5), Version: classVersion(row.PS5)},
			Convertible: row.Verified,
			Note:        row.Note,
		}
	}
	return gameapi.Compatibility{}
}

// ClassSuffix extracts the trailing class name from a full Unreal save
// class path (e.g. "/Game/.../BP_SaveGameObject_V7.BP_SaveGameObject_V7_C"
// -> "BP_SaveGameObject_V7_C"), or returns class unchanged if it has no
// "." (already just a suffix). This is what class_equivalence rows are
// matched against - see checkClassMap in gate.go.
func ClassSuffix(class string) string {
	if idx := strings.LastIndex(class, "."); idx >= 0 {
		return class[idx+1:]
	}
	return class
}

func classVersion(class string) string {
	if m := versionSuffix.FindStringSubmatch(class); len(m) == 2 {
		return "V" + m[1]
	}
	return ""
}

// GateChecks runs every portability check - the per-payload ones (magic,
// module, account-id, account-props, tail) on both sides plus the
// pairwise ones (class-map, package-version) - for one logical image. A
// tier-3 failure on either side aborts immediately as an error, since
// there's no parsed Info to run the pairwise checks against; tier-2
// failures come back as CheckResults for the caller to aggregate across
// every image before deciding whether to proceed.
func (e Engine) GateChecks(cfg Config, logical string, sourceRaw, targetRaw []byte, sourceSide, targetSide engine.Side, direction string, overrides map[string]bool) ([]engine.CheckResult, error) {
	sourceVerdict := e.Inspect(cfg, logical, sourceRaw, sourceSide, overrides)
	if sourceVerdict.Tier == engine.TierWrongFormat {
		return nil, fmt.Errorf("%s: %s", logical, sourceVerdict.Checks[0].Reason)
	}
	targetVerdict := e.Inspect(cfg, logical, targetRaw, targetSide, overrides)
	if targetVerdict.Tier == engine.TierWrongFormat {
		return nil, fmt.Errorf("%s: %s", logical, targetVerdict.Checks[0].Reason)
	}
	sourceInfo, err := gvas.Parse(sourceRaw, logical)
	if err != nil {
		return nil, err
	}
	targetInfo, err := gvas.Parse(targetRaw, logical)
	if err != nil {
		return nil, err
	}
	checks := append(append([]engine.CheckResult{}, sourceVerdict.Checks...), targetVerdict.Checks...)
	checks = append(checks, checkClassMap(cfg, logical, sourceInfo, targetInfo, direction, overrides), checkPackageVersion(cfg, sourceInfo, targetInfo, overrides))
	for i := range checks {
		checks[i].Logical = logical
	}
	return checks, nil
}

func blockingChecks(checks []engine.CheckResult) []engine.CheckResult {
	var out []engine.CheckResult
	for _, c := range checks {
		if !c.Passed && !c.Overridden {
			out = append(out, c)
		}
	}
	return out
}

func blockingError(blocked []engine.CheckResult) error {
	lines := make([]string, len(blocked))
	for i, c := range blocked {
		lines[i] = fmt.Sprintf("%s: %s [%s]", c.Logical, c.Reason, c.Check)
	}
	return fmt.Errorf("portability gate blocked this conversion; re-run with --allow <check> to bypass a specific one:\n  %s", strings.Join(lines, "\n  "))
}

// gateWarnings surfaces the checks that passed only because they were
// overridden, or that passed as an unverified class-equivalence
// candidate - both cases the run proceeded, but a human should see why.
func gateWarnings(checks []engine.CheckResult) []string {
	var out []string
	for _, c := range checks {
		switch {
		case c.Overridden:
			out = append(out, fmt.Sprintf("OVERRIDDEN %s/%s - %s", c.Logical, c.Check, c.Reason))
		case c.Warn:
			out = append(out, fmt.Sprintf("CANDIDATE %s/%s - %s", c.Logical, c.Check, c.Reason))
		}
	}
	return out
}

func overriddenCheckNames(checks []engine.CheckResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range checks {
		if c.Overridden && !seen[c.Check] {
			seen[c.Check] = true
			out = append(out, c.Check)
		}
	}
	return out
}

func gateManifest(checks []engine.CheckResult) []map[string]any {
	out := make([]map[string]any, len(checks))
	for i, c := range checks {
		out[i] = map[string]any{
			"logical":    c.Logical,
			"check":      c.Check,
			"tier":       int(c.Tier),
			"passed":     c.Passed,
			"overridden": c.Overridden,
			"warn":       c.Warn,
			"reason":     c.Reason,
		}
	}
	return out
}

func validatePCDir(pcDir string, images []ImageConfig) error {
	var missing []string
	for _, img := range images {
		path := filepath.Join(pcDir, img.PCFile)
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing PC save file(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func (e Engine) ConvertFromPS5(cfgAny any, ps5Payloads map[string][]byte, pcDir string, overrides map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if err := validatePCDir(pcDir, cfg.Images); err != nil {
		return gameapi.ConversionResult{}, err
	}
	pcData := map[string][]byte{}
	for _, img := range cfg.Images {
		data, err := os.ReadFile(filepath.Join(pcDir, img.PCFile))
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		pcData[img.Logical] = data
	}

	var allChecks []engine.CheckResult
	for _, img := range cfg.Images {
		ps5Data, ok := ps5Payloads[img.Logical]
		if !ok {
			return gameapi.ConversionResult{}, fmt.Errorf("missing PS5 payload for %s", img.Logical)
		}
		checks, err := e.GateChecks(cfg, img.Logical, ps5Data, pcData[img.Logical], engine.SidePS5, engine.SidePC, "ps5-to-pc", overrides)
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		allChecks = append(allChecks, checks...)
	}
	// The whole run aborts before any write if any required image fails a
	// non-overridden check: a partial graft across a multi-image game is
	// worse than none.
	if blocked := blockingChecks(allChecks); len(blocked) > 0 {
		return gameapi.ConversionResult{}, blockingError(blocked)
	}

	allowVersionMismatch := cfg.AllowPackageVersionMismatch || overrides[CheckPackageVersion]
	outputs := map[string][]byte{}
	manifest := map[string]any{
		"pc_dir":        pcDir,
		"compatibility": e.Compatibility(cfg),
		"gate":          gateManifest(allChecks),
	}
	warnings := gateWarnings(allChecks)
	for _, img := range cfg.Images {
		envelope, err := gvas.ConvertWithEnvelope(ps5Payloads[img.Logical], pcData[img.Logical], "Garlic PS5 "+img.Label, "PC "+img.Label+" template", gvas.EnvelopeOptions{AllowPackageVersionMismatch: allowVersionMismatch})
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		warnings = append(warnings, envelope.Warnings...)
		outputs[img.PCFile] = envelope.Data
		manifest[img.Logical] = map[string]any{
			"source":   envelope.Source,
			"template": envelope.Target,
			"result":   envelope.Result,
		}
	}
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings, OverriddenChecks: overriddenCheckNames(allChecks)}, nil
}

func (e Engine) ConvertToPS5(cfgAny any, pcDir string, ps5Templates map[string][]byte, overrides map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if err := validatePCDir(pcDir, cfg.Images); err != nil {
		return gameapi.ConversionResult{}, err
	}
	pcData := map[string][]byte{}
	for _, img := range cfg.Images {
		data, err := os.ReadFile(filepath.Join(pcDir, img.PCFile))
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		pcData[img.Logical] = data
	}

	var allChecks []engine.CheckResult
	for _, img := range cfg.Images {
		checks, err := e.GateChecks(cfg, img.Logical, pcData[img.Logical], ps5Templates[img.Logical], engine.SidePC, engine.SidePS5, "pc-to-ps5", overrides)
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		allChecks = append(allChecks, checks...)
	}
	if blocked := blockingChecks(allChecks); len(blocked) > 0 {
		return gameapi.ConversionResult{}, blockingError(blocked)
	}

	allowVersionMismatch := cfg.AllowPackageVersionMismatch || overrides[CheckPackageVersion]
	outputs := map[string][]byte{}
	manifest := map[string]any{
		"pc_dir":        pcDir,
		"compatibility": e.Compatibility(cfg),
		"gate":          gateManifest(allChecks),
	}
	warnings := gateWarnings(allChecks)
	for _, img := range cfg.Images {
		envelope, err := gvas.ConvertWithEnvelope(pcData[img.Logical], ps5Templates[img.Logical], "PC "+img.Label, "Garlic PS5 "+img.Label+" template", gvas.EnvelopeOptions{AllowPackageVersionMismatch: allowVersionMismatch})
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		warnings = append(warnings, envelope.Warnings...)
		outputs[img.SaveName] = envelope.Data
		manifest[img.Logical] = map[string]any{
			"source":          envelope.Source,
			"target_template": envelope.Target,
			"result":          envelope.Result,
			"save_name":       img.SaveName,
			"payload_name":    img.Payload,
		}
	}
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings, OverriddenChecks: overriddenCheckNames(allChecks)}, nil
}

func (Engine) InstallOutputs(cfgAny any, outputs map[string][]byte, pcDir string, backupDir string) error {
	cfg := cfgAny.(Config)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	for _, img := range cfg.Images {
		source := filepath.Join(pcDir, img.PCFile)
		backup := filepath.Join(backupDir, img.PCFile)
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			if err := util.CopyFile(source, backup); err != nil {
				return err
			}
		}
		if err := util.AtomicWrite(source, outputs[img.PCFile]); err != nil {
			return err
		}
	}
	return nil
}
