// Package larian implements engine.Engine for Larian's LSPK save
// container format, used by Baldur's Gate 3 (see lspk.go for the format
// itself). Unlike Clair's Unreal saves, a BG3 save has no fixed,
// config-known filenames at all: the PS5 save slot (sdimg_Save0001 vs
// sdimg_Save0002...), the PS5 image's .lsv filename, and the PC .lsv
// filename are all per-instance. Config.Images therefore declares its
// image with the Dynamic* flags set, and ResolvePayload/ResolvePCFile
// discover the real names at runtime (see bridge.go's resolution pass).
//
// ConvertToPS5 (PC -> PS5) has been confirmed end-to-end against a real
// PS5: a 39-hour Steam save was rebuilt and loaded successfully in-game
// (one non-fatal "tampering" warning, plausibly the PC save's newer game
// version - see docs/dev.md). ConvertFromPS5 (PS5 -> PC) uses the
// identical mechanism in the opposite direction but has not been
// field-tested in-game.
//
// Not yet implemented: mod-parity checking against a save's Profile
// image and build-order gate checks (both need parsers this package
// doesn't have - see Inspect's doc comment), and multi-image profiles
// (games/bg3.json declares exactly one save image).
package larian

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/util"
)

// ImageConfig describes one Garlic save image this game needs. Every
// field on a BG3 image is per-instance rather than config-known, so
// there are no static SaveName/PCFile/Payload fields here at all - only
// which Dynamic* resolution each needs (see gameapi.SaveImage).
type ImageConfig struct {
	Logical         string `json:"logical"`
	Label           string `json:"label"`
	DynamicSaveName bool   `json:"dynamic_save_name"`
	DynamicPayload  bool   `json:"dynamic_payload"`
	DynamicPCFile   bool   `json:"dynamic_pc_file"`
}

// Config is the engine_config block for a games/<key>.json profile using
// the "larian" engine.
type Config struct {
	Images []ImageConfig `json:"images"`
}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Name() string { return "larian" }

// OverrideTokens is empty: no portability-gate checks beyond Inspect's
// tier-3 magic/required-members checks exist yet for this engine, so
// there's nothing a tier-2 --allow could bypass.
func (Engine) OverrideTokens() []string { return nil }

func (Engine) ParseConfig(raw json.RawMessage) (any, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid larian engine_config: %w", err)
	}
	if len(cfg.Images) == 0 {
		return nil, fmt.Errorf("larian engine_config has no images")
	}
	for _, img := range cfg.Images {
		if img.Logical == "" {
			return nil, fmt.Errorf("larian engine_config image entries require logical")
		}
	}
	return cfg, nil
}

func (Engine) Images(cfgAny any) []gameapi.SaveImage {
	cfg := cfgAny.(Config)
	out := make([]gameapi.SaveImage, len(cfg.Images))
	for i, img := range cfg.Images {
		out[i] = gameapi.SaveImage{
			Logical:         img.Logical,
			Label:           img.Label,
			DynamicSaveName: img.DynamicSaveName,
			DynamicPayload:  img.DynamicPayload,
			DynamicPCFile:   img.DynamicPCFile,
		}
	}
	return out
}

func (Engine) Compatibility(any) gameapi.Compatibility {
	return gameapi.Compatibility{
		PC:          gameapi.CompatibilitySide{Platform: "Steam"},
		PS5:         gameapi.CompatibilitySide{Platform: "PS5"},
		Convertible: true,
		Note:        "PC->PS5 confirmed working against a real save (SaveInfo.json Platform rewrite + full LSPK rebuild). PS5->PC uses the same mechanism but hasn't been field-tested in-game yet.",
	}
}

// ResolvePayload finds the single ".lsv" file among a mounted BG3 save
// image's contents - that's the actual save data; every other file
// (sce_sys/*, the .WebP thumbnail) is either the PS5 container itself or
// cosmetic. Errors if there isn't exactly one.
func (Engine) ResolvePayload(_ any, image gameapi.SaveImage, mountedFileNames []string) (string, error) {
	name, err := findSingleLSV(mountedFileNames, fmt.Sprintf("%s: mounted save image", image.Logical))
	if err != nil {
		return "", err
	}
	return name, nil
}

// ResolvePCFile finds the single ".lsv" file in the PC save directory -
// a real Steam BG3 save folder for one save is exactly a ".lsv" plus a
// ".WebP" with a matching name (confirmed against a real "Ruined
// Battlefield - 39h 05m.lsv" export). Errors if there isn't exactly one.
func (Engine) ResolvePCFile(_ any, image gameapi.SaveImage, pcDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(pcDir, "*.lsv"))
	if err != nil {
		return "", err
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	name, err := findSingleLSV(names, fmt.Sprintf("%s: %s", image.Logical, pcDir))
	if err != nil {
		return "", err
	}
	return name, nil
}

func findSingleLSV(names []string, context string) (string, error) {
	var matches []string
	for _, name := range names {
		if strings.HasSuffix(name, ".lsv") {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%s: no .lsv save file found", context)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%s: multiple .lsv save files found (%s); expected exactly one", context, strings.Join(matches, ", "))
	}
	return matches[0], nil
}

const (
	CheckMagic           = "magic"
	CheckRequiredMembers = "required-members"
)

// requiredMembers are the files every real BG3 save (both platforms) has
// been observed to contain. Confirmed against real PS5 and PC saves; see
// docs/dev.md. Checking for these is as far as this Inspect goes today -
// the checks the original design spec describes beyond this
// (osiris-version, lsof-version, mod-parity, build-order) need LSOF and
// Osiris parsers this package doesn't have yet.
var requiredMembers = []string{"meta.lsf", "SaveInfo.json", "StorySave.bin", "Globals.lsf"}

func (Engine) Inspect(_ any, logical string, payload []byte, _ engine.Side, _ map[string]bool) engine.Verdict {
	archive, err := Parse(payload)
	if err != nil {
		return engine.Verdict{
			Tier: engine.TierWrongFormat,
			Checks: []engine.CheckResult{{
				Logical: logical,
				Check:   CheckMagic,
				Tier:    engine.TierWrongFormat,
				Passed:  false,
				Reason:  err.Error(),
			}},
		}
	}

	checks := []engine.CheckResult{{Logical: logical, Check: CheckMagic, Tier: engine.TierWrongFormat, Passed: true}}
	var missing []string
	for _, name := range requiredMembers {
		if _, ok := archive.Find(name); !ok {
			missing = append(missing, name)
		}
	}
	membersCheck := engine.CheckResult{Logical: logical, Check: CheckRequiredMembers, Tier: engine.TierWrongFormat, Passed: len(missing) == 0}
	if len(missing) > 0 {
		membersCheck.Reason = fmt.Sprintf("missing required member(s): %s", strings.Join(missing, ", "))
	}
	checks = append(checks, membersCheck)

	verdict := engine.Verdict{Portable: true, Checks: checks}
	for _, c := range checks {
		if !c.Passed {
			verdict.Portable = false
			verdict.Tier = engine.TierWrongFormat
		}
	}
	return verdict
}

// platformFieldPattern matches SaveInfo.json's "Platform": "..." field.
// A targeted regex replace, not a JSON parse/re-marshal round-trip,
// deliberately preserves BG3's own pretty-printing (key order, spacing)
// exactly - proven against a real game-accepted save this session; a
// generic JSON marshaler reformatting the file is an unnecessary risk
// this doesn't need to take.
var platformFieldPattern = regexp.MustCompile(`("Platform"\s*:\s*)"[^"]*"`)

func rewritePlatform(jsonText []byte, newValue string) ([]byte, error) {
	if !platformFieldPattern.Match(jsonText) {
		return nil, fmt.Errorf(`"Platform" field not found in SaveInfo.json`)
	}
	return platformFieldPattern.ReplaceAll(jsonText, []byte(`${1}"`+newValue+`"`)), nil
}

// rebuildWithPlatform parses an LSPK archive, rewrites SaveInfo.json's
// Platform field, and rebuilds the whole archive via Build (not
// WithReplacedEntry - a full graft always replaces every entry, so
// there's no "existing archive plus one changed entry" to patch).
// Confirmed live: this is the exact mechanism (64-byte-aligned entries,
// real LZ4 table compression, physical-order MD5Recompute - see
// lspk.go) that produced a save the game accepted.
func rebuildWithPlatform(data []byte, newPlatform string) ([]byte, error) {
	archive, err := Parse(data)
	if err != nil {
		return nil, err
	}
	specs := make([]EntrySpec, len(archive.Entries))
	for i, e := range archive.Entries {
		content, err := archive.ReadDecompressed(e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name, err)
		}
		if e.Name == "SaveInfo.json" {
			patched, err := rewritePlatform(content, newPlatform)
			if err != nil {
				return nil, err
			}
			content = patched
		}
		specs[i] = EntrySpec{Name: e.Name, Content: content, Compression: e.Compression()}
	}
	return Build(specs, MD5Recompute)
}

// ConvertToPS5 rewrites a PC (Steam) save into a PS5-shaped payload:
// SaveInfo.json's Platform "Steam" -> "Prospero", everything else
// (meta.lsf, StorySave.bin, Globals.lsf, LevelCache/*) carried through
// unchanged. Confirmed working end-to-end against a real PS5 - see the
// package doc comment.
func (Engine) ConvertToPS5(_ any, images []gameapi.SaveImage, pcDir string, _ map[string][]byte, _ map[string]bool) (gameapi.ConversionResult, error) {
	if len(images) != 1 {
		return gameapi.ConversionResult{}, fmt.Errorf("larian engine currently supports exactly one save image per profile, got %d", len(images))
	}
	image := images[0]
	if image.PCFile == "" {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: PC save file was not resolved", image.Logical)
	}
	if image.SaveName == "" {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: PS5 save name was not resolved (pass --ps5-save-name)", image.Logical)
	}
	pcData, err := os.ReadFile(filepath.Join(pcDir, image.PCFile))
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	rebuilt, err := rebuildWithPlatform(pcData, "Prospero")
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.PCFile, err)
	}

	outputs := map[string][]byte{image.SaveName: rebuilt}
	manifest := map[string]any{
		"pc_file":  image.PCFile,
		"ps5_save": image.SaveName,
		"lsv_name": image.Payload,
		"note":     "Uploads only the .lsv; the .WebP thumbnail and OS-level save-list display name aren't updated by this tool, so the save list may show stale text/art until the game next saves to this slot.",
	}
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest}, nil
}

// ConvertFromPS5 rewrites a PS5 save into a PC (Steam)-shaped file:
// SaveInfo.json's Platform "Prospero" -> "Steam", everything else
// carried through unchanged. Uses the identical mechanism as
// ConvertToPS5 (proven against a real PS5 in that direction) but this
// direction has not itself been field-tested in-game yet.
func (Engine) ConvertFromPS5(_ any, images []gameapi.SaveImage, ps5Payloads map[string][]byte, _ string, _ map[string]bool) (gameapi.ConversionResult, error) {
	if len(images) != 1 {
		return gameapi.ConversionResult{}, fmt.Errorf("larian engine currently supports exactly one save image per profile, got %d", len(images))
	}
	image := images[0]
	ps5Data, ok := ps5Payloads[image.Logical]
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("missing PS5 payload for %s", image.Logical)
	}
	rebuilt, err := rebuildWithPlatform(ps5Data, "Steam")
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}

	outputFile := image.Payload
	if outputFile == "" {
		outputFile = image.SaveName + ".lsv"
	}
	outputs := map[string][]byte{outputFile: rebuilt}
	manifest := map[string]any{
		"ps5_save": image.SaveName,
		"pc_file":  outputFile,
		"note":     "PS5->PC (Prospero->Steam) direction has not been field-tested in-game; the PC->PS5 direction has (see docs/dev.md).",
	}
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest}, nil
}

// InstallOutputs writes converted output(s) into pcDir, backing up any
// file it would overwrite first. Unlike unreal's InstallOutputs (which
// derives filenames from cfg.Images, always fixed), outputs' own keys
// are iterated directly - they're already the real, resolved filenames
// (see ConvertFromPS5) - and a missing source file is not backed up
// (installing a save that never existed in pcDir before is a legitimate
// case here, unlike Clair where the target always pre-exists).
func (Engine) InstallOutputs(_ any, outputs map[string][]byte, pcDir string, backupDir string) error {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	for name, data := range outputs {
		source := filepath.Join(pcDir, name)
		backup := filepath.Join(backupDir, name)
		if _, err := os.Stat(source); err == nil {
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err := util.CopyFile(source, backup); err != nil {
					return err
				}
			}
		}
		if err := util.AtomicWrite(source, data); err != nil {
			return err
		}
	}
	return nil
}
