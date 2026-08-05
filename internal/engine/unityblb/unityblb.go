package unityblb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/util"
)

// ImageConfig describes one Garlic save image this game needs. Unlike
// Baldur's Gate 3 (internal/engine/larian), a Subnautica save slot's
// identity is fixed by convention - "sdimg_slot0000" on PS5,
// "slot0000" under SNAppData/SavedGames on PC - so every field here is
// config-known, the same way internal/engine/unreal's images are.
type ImageConfig struct {
	Logical string `json:"logical"`
	// SaveName is the Garlic save image name, e.g. "sdimg_slot0000".
	SaveName string `json:"save_name"`
	Label    string `json:"label"`
	// PCDir is the slot's directory name under the game's PC save root
	// (e.g. "slot0000"), not a single file - a Subnautica save is a whole
	// directory tree (gameinfo.json, global-objects.bin,
	// scene-objects.bin, screenshot.jpg, CellsCache/*.zip). It's stored in
	// gameapi.SaveImage's PCFile field; bridge.go's backup path handles a
	// directory-shaped PCFile (see util.CopyDir).
	PCDir string `json:"pc_dir"`
	// Payload is the filename inside the mounted Garlic image, e.g.
	// "slot0000.blb".
	Payload string `json:"payload"`
}

// Config is the engine_config block for a games/<key>.json profile using
// the "unityblb" engine.
type Config struct {
	Images []ImageConfig `json:"images"`
}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Name() string { return "unityblb" }

const CheckContainer = "container"

// CheckProtoVersion is the one tier-2 check this engine runs: a
// mismatched gameinfo.json protoBufVersion between the two sides means a
// different game build may not deserialize the transplanted save
// correctly.
const CheckProtoVersion = "proto-version"

func (Engine) OverrideTokens() []string { return []string{CheckProtoVersion} }

func (Engine) ParseConfig(raw json.RawMessage) (any, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid unityblb engine_config: %w", err)
	}
	if len(cfg.Images) == 0 {
		return nil, fmt.Errorf("unityblb engine_config has no images")
	}
	for _, img := range cfg.Images {
		if img.Logical == "" || img.SaveName == "" || img.PCDir == "" || img.Payload == "" {
			return nil, fmt.Errorf("unityblb engine_config image entries require logical, save_name, pc_dir, and payload")
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
			PCFile:   img.PCDir,
			Payload:  img.Payload,
		}
	}
	return out
}

// ResolvePayload and ResolvePCFile are no-ops: every image's SaveName/
// PCDir/Payload is a fixed, known-ahead-of-time value from engine_config
// (no Dynamic* flag is ever set on an image this engine produces).
func (Engine) ResolvePayload(_ any, image gameapi.SaveImage, _ []string) (string, error) {
	return image.Payload, nil
}

func (Engine) ResolvePCFile(_ any, image gameapi.SaveImage, _ string) (string, error) {
	return image.PCFile, nil
}

func (Engine) Compatibility(any) gameapi.Compatibility {
	return gameapi.Compatibility{
		PC:          gameapi.CompatibilitySide{Platform: "Steam"},
		PS5:         gameapi.CompatibilitySide{Platform: "PS5"},
		Convertible: true,
		Note:        "Unencrypted gzip+TLV container, no proprietary class/versioning system and no platform field found in the save data itself - confirmed byte-identical round trip (both directions) against a real PS5 save and its PC equivalent. See docs/subnautica.md.",
	}
}

type gameInfo struct {
	ProtoBufVersion int `json:"protoBufVersion"`
}

// stripBOM removes a leading UTF-8 byte-order mark: a real gameinfo.json
// (both PC and PS5) has been observed to start with one, which
// encoding/json otherwise rejects as invalid.
func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
}

// Inspect validates structural sanity for one side. The PS5 side's
// payload is the raw fetched .blb bytes; the PC side's payload is just
// its slot directory's gameinfo.json bytes (the one small, universally
// present file that carries the compatibility-relevant metadata) - see
// ConvertFromPS5/ConvertToPS5, which read it directly rather than trying
// to funnel an entire directory tree through this single-[]byte method.
func (Engine) Inspect(_ any, logical string, payload []byte, side engine.Side, _ map[string]bool) engine.Verdict {
	check := engine.CheckResult{Logical: logical, Check: CheckContainer, Tier: engine.TierWrongFormat}
	var checkErr error
	if side == engine.SidePS5 {
		_, checkErr = Decode(payload)
	} else {
		var gi gameInfo
		if err := json.Unmarshal(stripBOM(payload), &gi); err != nil {
			checkErr = fmt.Errorf("gameinfo.json is not valid JSON: %w", err)
		} else if gi.ProtoBufVersion == 0 {
			checkErr = errors.New("gameinfo.json missing protoBufVersion")
		}
	}
	if checkErr != nil {
		check.Passed = false
		check.Reason = checkErr.Error()
		return engine.Verdict{Tier: engine.TierWrongFormat, Checks: []engine.CheckResult{check}}
	}
	check.Passed = true
	return engine.Verdict{Portable: true, Checks: []engine.CheckResult{check}}
}

// checkProtoVersion is the pairwise tier-2 check: same idea as unreal's
// checkPackageVersion, just comparing gameinfo.json's protoBufVersion
// field instead of a GVAS package version.
func checkProtoVersion(logical string, sourceJSON, targetJSON []byte, overrides map[string]bool) engine.CheckResult {
	check := engine.CheckResult{Logical: logical, Check: CheckProtoVersion, Tier: engine.TierBlocked}
	var source, target gameInfo
	switch {
	case json.Unmarshal(stripBOM(sourceJSON), &source) != nil:
		check.Reason = "could not parse source gameinfo.json"
	case json.Unmarshal(stripBOM(targetJSON), &target) != nil:
		check.Reason = "could not parse target gameinfo.json"
	case source.ProtoBufVersion != target.ProtoBufVersion:
		check.Reason = fmt.Sprintf("protoBufVersion mismatch (%d vs %d) - a different game version may not deserialize this save correctly", source.ProtoBufVersion, target.ProtoBufVersion)
	default:
		check.Passed = true
		return check
	}
	if overrides[CheckProtoVersion] {
		check.Overridden = true
	}
	return check
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

func gateWarnings(checks []engine.CheckResult) []string {
	var out []string
	for _, c := range checks {
		if c.Overridden {
			out = append(out, fmt.Sprintf("OVERRIDDEN %s/%s - %s", c.Logical, c.Check, c.Reason))
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

// ConvertFromPS5 unpacks a PS5 .blb container into the equivalent PC
// directory layout: loose files carried through unchanged, and
// CellsCache's flat per-cell entries regrouped into PC-shaped
// "baked-batch-cells-<batch>-grp0.zip" files (see
// groupCellsCacheIntoZips). Output keys are "<pcDir>/<relative path>",
// e.g. "slot0000/CellsCache/baked-batch-cells-11-grp0.zip".
func (e Engine) ConvertFromPS5(cfgAny any, _ []gameapi.SaveImage, ps5Payloads map[string][]byte, pcDir string, overrides map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	outputs := map[string][]byte{}
	manifest := map[string]any{"pc_dir": pcDir}
	var allChecks []engine.CheckResult

	for _, img := range cfg.Images {
		ps5Data, ok := ps5Payloads[img.Logical]
		if !ok {
			return gameapi.ConversionResult{}, fmt.Errorf("missing PS5 payload for %s", img.Logical)
		}
		verdict := e.Inspect(cfg, img.Logical, ps5Data, engine.SidePS5, overrides)
		if verdict.Tier == engine.TierWrongFormat {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %s", img.Logical, verdict.Checks[0].Reason)
		}

		entries, err := Decode(ps5Data)
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}

		// Pairwise proto-version check against whatever's already in the
		// PC directory, if anything - a first-time install has nothing to
		// compare against, which is fine.
		if ps5Info, ok := Find(entries, "gameinfo.json"); ok {
			if pcInfo, err := os.ReadFile(filepath.Join(pcDir, img.PCDir, "gameinfo.json")); err == nil {
				allChecks = append(allChecks, checkProtoVersion(img.Logical, ps5Info, pcInfo, overrides))
			}
		}

		plain, cellsCache, err := splitCellsCacheEntries(entries)
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}
		zips, err := groupCellsCacheIntoZips(cellsCache)
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}

		for _, e := range plain {
			outputs[img.PCDir+"/"+e.Name] = e.Data
		}
		for _, e := range zips {
			outputs[img.PCDir+"/"+e.Name] = e.Data
		}
		manifest[img.Logical] = map[string]any{
			"ps5_save": img.SaveName,
			"pc_dir":   img.PCDir,
			"files":    len(plain) + len(zips),
		}
	}

	if blocked := blockingChecks(allChecks); len(blocked) > 0 {
		return gameapi.ConversionResult{}, blockingError(blocked)
	}
	warnings := gateWarnings(allChecks)
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings, OverriddenChecks: overriddenCheckNames(allChecks)}, nil
}

// ConvertToPS5 packs a PC save directory into a PS5-shaped .blb: loose
// files carried through unchanged, and every CellsCache/*.zip's members
// flattened into individual TLV entries (see readPCDirEntries). Output
// keys are the image's SaveName, matching unreal/larian's convention.
func (e Engine) ConvertToPS5(cfgAny any, _ []gameapi.SaveImage, pcDir string, ps5Templates map[string][]byte, overrides map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	outputs := map[string][]byte{}
	manifest := map[string]any{"pc_dir": pcDir}
	var allChecks []engine.CheckResult

	for _, img := range cfg.Images {
		pcDirPath := filepath.Join(pcDir, img.PCDir)
		if info, err := os.Stat(pcDirPath); err != nil || !info.IsDir() {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: missing PC save directory: %s", img.Logical, pcDirPath)
		}

		pcGameInfo, err := os.ReadFile(filepath.Join(pcDirPath, "gameinfo.json"))
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}
		verdict := e.Inspect(cfg, img.Logical, pcGameInfo, engine.SidePC, overrides)
		if verdict.Tier == engine.TierWrongFormat {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %s", img.Logical, verdict.Checks[0].Reason)
		}

		if template, ok := ps5Templates[img.Logical]; ok {
			if templateEntries, err := Decode(template); err == nil {
				if templateInfo, ok := Find(templateEntries, "gameinfo.json"); ok {
					allChecks = append(allChecks, checkProtoVersion(img.Logical, pcGameInfo, templateInfo, overrides))
				}
			}
		}

		entries, err := readPCDirEntries(pcDirPath)
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}
		encoded, err := Encode(entries)
		if err != nil {
			return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", img.Logical, err)
		}

		outputs[img.SaveName] = encoded
		manifest[img.Logical] = map[string]any{
			"pc_dir":       img.PCDir,
			"ps5_save":     img.SaveName,
			"payload_name": img.Payload,
			"files":        len(entries),
		}
	}

	if blocked := blockingChecks(allChecks); len(blocked) > 0 {
		return gameapi.ConversionResult{}, blockingError(blocked)
	}
	warnings := gateWarnings(allChecks)
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings, OverriddenChecks: overriddenCheckNames(allChecks)}, nil
}

// InstallOutputs replaces each image's PC slot directory wholesale rather
// than merging individual files in: a leftover CellsCache zip from a
// previous, differently-explored save could otherwise sit alongside the
// new one, mixing world state from two unrelated saves. The pre-existing
// directory (if any) is backed up first via util.CopyDir - bridge.go's
// own BackupCurrentSaves has typically already done this before
// conversion runs, but this is the safety net for a slot that didn't
// exist as a config-known image at that point.
func (Engine) InstallOutputs(cfgAny any, outputs map[string][]byte, pcDir string, backupDir string) error {
	cfg := cfgAny.(Config)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}

	bySlot := map[string]map[string][]byte{}
	for name, data := range outputs {
		slot, rel, ok := strings.Cut(name, "/")
		if !ok {
			return fmt.Errorf("unexpected unityblb output path %q (want <slot>/<file>)", name)
		}
		if bySlot[slot] == nil {
			bySlot[slot] = map[string][]byte{}
		}
		bySlot[slot][rel] = data
	}

	for _, img := range cfg.Images {
		files, ok := bySlot[img.PCDir]
		if !ok {
			continue
		}
		dest := filepath.Join(pcDir, img.PCDir)
		backup := filepath.Join(backupDir, img.PCDir)
		if _, err := os.Stat(dest); err == nil {
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err := util.CopyDir(dest, backup); err != nil {
					return err
				}
			}
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
		for rel, data := range files {
			if err := util.AtomicWrite(filepath.Join(dest, rel), data); err != nil {
				return err
			}
		}
	}
	return nil
}
