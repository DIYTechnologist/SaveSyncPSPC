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
// pair for a logical image. See Config.ClassEquivalence.
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
	// Module is the Unreal module the save classes belong to, e.g.
	// "Sandfall" for "/Script/Sandfall.BP_SaveGameObject_V8_C". Reserved
	// for the portability gate's module-match check; not yet enforced.
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
			PC:          gameapi.CompatibilitySide{Platform: "Steam", GameplayClassSuffix: classSuffix(row.PC), Version: classVersion(row.PC)},
			PS5:         gameapi.CompatibilitySide{Platform: "PS5", GameplayClassSuffix: classSuffix(row.PS5), Version: classVersion(row.PS5)},
			Convertible: row.Verified,
			Note:        row.Note,
		}
	}
	return gameapi.Compatibility{}
}

func classSuffix(class string) string {
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

// classWarnings checks the actual source/target save classes for a logical
// image against its class_equivalence row (if any). A miss only warns in
// this phase; the portability gate that turns a miss into a hard refusal
// is a later addition (see docs/dev.md).
func classWarnings(cfg Config, logical string, source, target gvas.Info, direction string) []string {
	var expectedPC, expectedPS5 string
	found := false
	for _, row := range cfg.ClassEquivalence {
		if row.Logical == logical {
			expectedPC, expectedPS5 = row.PC, row.PS5
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	var warnings []string
	var sourceExpected, targetExpected string
	switch direction {
	case "ps5-to-pc":
		sourceExpected, targetExpected = expectedPS5, expectedPC
	case "pc-to-ps5":
		sourceExpected, targetExpected = expectedPC, expectedPS5
	}
	if source.SaveClass != sourceExpected {
		warnings = append(warnings, fmt.Sprintf("Expected %s class %s, got %s", logical, sourceExpected, source.SaveClass))
	}
	if target.SaveClass != targetExpected {
		warnings = append(warnings, fmt.Sprintf("Expected %s class %s, got %s", logical, targetExpected, target.SaveClass))
	}
	return warnings
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

func (e Engine) ConvertFromPS5(cfgAny any, ps5Payloads map[string][]byte, pcDir string) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if err := validatePCDir(pcDir, cfg.Images); err != nil {
		return gameapi.ConversionResult{}, err
	}
	outputs := map[string][]byte{}
	manifest := map[string]any{
		"pc_dir":        pcDir,
		"compatibility": e.Compatibility(cfg),
	}
	var warnings []string
	for _, img := range cfg.Images {
		pcData, err := os.ReadFile(filepath.Join(pcDir, img.PCFile))
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		ps5Data, ok := ps5Payloads[img.Logical]
		if !ok {
			return gameapi.ConversionResult{}, fmt.Errorf("missing PS5 payload for %s", img.Logical)
		}
		envelope, err := gvas.ConvertWithEnvelope(ps5Data, pcData, "Garlic PS5 "+img.Label, "PC "+img.Label+" template", gvas.EnvelopeOptions{AllowPackageVersionMismatch: cfg.AllowPackageVersionMismatch})
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		warnings = append(warnings, envelope.Warnings...)
		warnings = append(warnings, classWarnings(cfg, img.Logical, envelope.Source, envelope.Target, "ps5-to-pc")...)
		outputs[img.PCFile] = envelope.Data
		manifest[img.Logical] = map[string]any{
			"source":   envelope.Source,
			"template": envelope.Target,
			"result":   envelope.Result,
		}
	}
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings}, nil
}

func (e Engine) ConvertToPS5(cfgAny any, pcDir string, ps5Templates map[string][]byte) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if err := validatePCDir(pcDir, cfg.Images); err != nil {
		return gameapi.ConversionResult{}, err
	}
	outputs := map[string][]byte{}
	manifest := map[string]any{
		"pc_dir":        pcDir,
		"compatibility": e.Compatibility(cfg),
	}
	var warnings []string
	for _, img := range cfg.Images {
		pcData, err := os.ReadFile(filepath.Join(pcDir, img.PCFile))
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		envelope, err := gvas.ConvertWithEnvelope(pcData, ps5Templates[img.Logical], "PC "+img.Label, "Garlic PS5 "+img.Label+" template", gvas.EnvelopeOptions{AllowPackageVersionMismatch: cfg.AllowPackageVersionMismatch})
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		warnings = append(warnings, envelope.Warnings...)
		warnings = append(warnings, classWarnings(cfg, img.Logical, envelope.Source, envelope.Target, "pc-to-ps5")...)
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
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings}, nil
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
