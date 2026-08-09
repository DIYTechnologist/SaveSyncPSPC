// Package reengine implements engine.Engine for Capcom's RE Engine
// "DSSS" save containers, as used by Resident Evil 2 (2019). The format
// work itself lives in internal/reengine; this package is the adapter
// that plugs it into a games/<key>.json profile.
//
// PC -> PS5 conversion is confirmed working against a real PS5 (two
// saves: a fresh autosave and a 2.5 MB deep-progress manual slot). The
// PS5 -> PC direction uses the same mechanism reversed and is
// implemented, but has not been confirmed in-game.
//
// Like Baldur's Gate 3 and unlike Clair, a save's identity isn't fully
// config-known: which slots exist differs per player, so the profile
// declares one image with DynamicSaveName/DynamicPayload/DynamicPCFile
// set and the target slot is named at runtime via --ps5-save-name (see
// bridge.go's resolution pass). The PC-side filename is *derived* from
// that slot rather than discovered, because a PC save directory holds
// every slot's file side by side and only the matching one may be used -
// a save carries its slot number internally and must land in the
// container for that same slot.
package reengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
	re "savesyncpspc/internal/reengine"
	"savesyncpspc/internal/util"
)

// ImageConfig describes the single save image a RE Engine profile needs.
// Every identifying field is per-instance, so there are no static
// SaveName/PCFile/Payload values here - only which resolution each needs.
type ImageConfig struct {
	Logical         string `json:"logical"`
	Label           string `json:"label"`
	DynamicSaveName bool   `json:"dynamic_save_name"`
	DynamicPayload  bool   `json:"dynamic_payload"`
	DynamicPCFile   bool   `json:"dynamic_pc_file"`
}

// titles maps a profile's "title" value to the per-title format facts
// (key, platform-identity class, PS5 container shape) in
// internal/reengine. Adding a new title means adding a TitleConfig there
// and a line here - no other code in this package is title-specific,
// *except* "re4": its PS5 side is plain Blowfish like RE2 (so its entry
// here is still used by Inspect/checkSlot for that side), but its Steam
// side uses a completely different cipher (Lime, not Blowfish at all),
// so ConvertToPS5/ConvertFromPS5 special-case cfg.Title == "re4" and
// call re.ConvertRE4PCToPS5/ConvertRE4PS5ToPC directly instead of going
// through TitleConfig's (Blowfish-only) methods.
var titles = map[string]re.TitleConfig{
	"re2":     re.RE2,
	"re3":     re.RE3,
	"re4":     {Key: re.KeyRE4, PlatformClass: re.RE4PlatformClass},
	"re7":     re.RE7,
	"village": re.Village,
}

// Config is the engine_config block for a "reengine" profile.
type Config struct {
	// SaveNamePrefix is the fixed part of this title's Garlic save names,
	// e.g. "sdimg_SAVESERVICE-LINE-0-" for RE2; whatever follows it
	// identifies the slot.
	SaveNamePrefix string `json:"save_name_prefix"`
	// Title selects this profile's format facts from titles above
	// (currently "re2", "re3", "re4", "re7", or "village").
	Title  string        `json:"title"`
	Images []ImageConfig `json:"images"`
}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Name() string { return "reengine" }

// OverrideTokens is empty: this engine's checks are all structural
// (tier-3), so there is nothing a tier-2 --allow could bypass.
func (Engine) OverrideTokens() []string { return nil }

func (Engine) ParseConfig(raw json.RawMessage) (any, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid reengine engine_config: %w", err)
	}
	if cfg.SaveNamePrefix == "" {
		return nil, fmt.Errorf("reengine engine_config requires save_name_prefix")
	}
	if _, ok := titles[cfg.Title]; !ok {
		return nil, fmt.Errorf("reengine engine_config: unknown or missing title %q", cfg.Title)
	}
	if len(cfg.Images) != 1 {
		return nil, fmt.Errorf("reengine engine_config needs exactly one image, got %d", len(cfg.Images))
	}
	if cfg.Images[0].Logical == "" {
		return nil, fmt.Errorf("reengine engine_config image requires logical")
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
		Note:        "PC->PS5 confirmed loading on a real PS5. The save-select text stays stale until the game next saves to that slot (it lives in the PS5's own save registration, not the file). PS5->PC uses the same mechanism reversed but hasn't been confirmed in-game.",
	}
}

// profileSlotToken is the slot suffix of the global profile/settings
// save. Converting that file was observed to crash the game at startup,
// so this engine refuses it outright rather than letting a caller
// discover that on a console.
const profileSlotToken = "-1"

// slotToken extracts the part of a Garlic save name that identifies the
// slot, e.g. "1Slot" from "sdimg_SAVESERVICE-LINE-0-1Slot".
func slotToken(cfg Config, saveName string) (string, error) {
	if !strings.HasPrefix(saveName, cfg.SaveNamePrefix) {
		return "", fmt.Errorf("save name %q doesn't start with this game's prefix %q", saveName, cfg.SaveNamePrefix)
	}
	token := strings.TrimPrefix(saveName, cfg.SaveNamePrefix)
	if token == "" {
		return "", fmt.Errorf("save name %q has no slot part after the prefix", saveName)
	}
	return token, nil
}

// fileForSlot maps a slot token to its save filename. RE2 pads the slot
// number to three digits and keeps the "Slot" suffix that distinguishes
// a manual save from the autosave: slot 0 -> data000.bin, slot 1 ->
// data001Slot.bin, slot 21 -> data021Slot.bin.
func fileForSlot(token string) (string, error) {
	if token == profileSlotToken {
		return "", fmt.Errorf("slot %q is the global profile/settings save; converting it was observed to crash the game at startup, so it is refused", token)
	}
	numeric := strings.TrimSuffix(token, "Slot")
	n, err := strconv.Atoi(numeric)
	if err != nil {
		return "", fmt.Errorf("slot %q isn't a recognised slot name", token)
	}
	if n < 0 {
		return "", fmt.Errorf("slot %q has a negative index", token)
	}
	if strings.HasSuffix(token, "Slot") {
		return fmt.Sprintf("data%03dSlot.bin", n), nil
	}
	return fmt.Sprintf("data%03d.bin", n), nil
}

// ResolvePayload picks the save file out of a mounted container. The
// container holds exactly one .bin alongside sce_sys/*, so this both
// finds it and confirms the container is shaped as expected.
func (Engine) ResolvePayload(_ any, image gameapi.SaveImage, mountedFileNames []string) (string, error) {
	var matches []string
	for _, name := range mountedFileNames {
		if strings.HasPrefix(name, "sce_sys/") || name == "sce_sys" {
			continue
		}
		if strings.HasSuffix(name, ".bin") {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%s: no .bin save file in the mounted container", image.Logical)
	default:
		return "", fmt.Errorf("%s: expected one .bin in the mounted container, found %d (%s)",
			image.Logical, len(matches), strings.Join(matches, ", "))
	}
}

// ResolvePCFile derives the PC filename from the resolved PS5 slot,
// rather than discovering it: a PC save directory holds every slot's
// file at once, and only the one belonging to the target slot may be
// used. Deriving it means a mismatch is impossible by construction.
func (Engine) ResolvePCFile(cfgAny any, image gameapi.SaveImage, pcDir string) (string, error) {
	cfg := cfgAny.(Config)
	if image.SaveName == "" {
		return "", fmt.Errorf("%s: PS5 save slot isn't known yet; pass --ps5-save-name", image.Logical)
	}
	token, err := slotToken(cfg, image.SaveName)
	if err != nil {
		return "", fmt.Errorf("%s: %w", image.Logical, err)
	}
	name, err := fileForSlot(token)
	if err != nil {
		return "", fmt.Errorf("%s: %w", image.Logical, err)
	}
	if _, err := os.Stat(filepath.Join(pcDir, name)); err != nil {
		return "", fmt.Errorf("%s: slot %q needs %s in the PC save directory: %w", image.Logical, token, name, err)
	}
	return name, nil
}

const (
	CheckMagic = "magic"
	CheckSlot  = "slot-match"
)

// Inspect checks that a payload is a DSSS save this engine can read.
// Both checks are tier-3: a payload failing either isn't the thing the
// engine expects at all, so neither is overridable.
func (Engine) Inspect(cfgAny any, logical string, payload []byte, side engine.Side, _ map[string]bool) engine.Verdict {
	cfg := cfgAny.(Config)

	// RE4's Steam side uses Lime, not Blowfish - re.Decode doesn't apply
	// at all, and a full LimeDecode needs a Steam ID this method doesn't
	// have. A structural-only header check is all that's possible (and
	// all that's needed: save-sync inspect already refuses dynamic-image
	// engines, which this one is, so nothing currently calls this for
	// re4's PC side expecting more).
	if cfg.Title == "re4" && side == engine.SidePC {
		check := engine.CheckResult{Logical: logical, Check: CheckMagic, Tier: engine.TierWrongFormat}
		if err := re.LimeHeaderValid(payload); err != nil {
			check.Reason = err.Error()
			return engine.Verdict{Tier: engine.TierWrongFormat, Checks: []engine.CheckResult{check}}
		}
		check.Passed = true
		return engine.Verdict{Portable: true, Checks: []engine.CheckResult{check}}
	}

	title := titles[cfg.Title]
	key := title.Key
	if side == engine.SidePS5 && title.PS5Unencrypted {
		key = nil
	}
	dec, err := re.Decode(payload, key)
	if err != nil {
		return engine.Verdict{
			Tier: engine.TierWrongFormat,
			Checks: []engine.CheckResult{{
				Logical: logical, Check: CheckMagic, Tier: engine.TierWrongFormat,
				Passed: false, Reason: err.Error(),
			}},
		}
	}
	checks := []engine.CheckResult{{
		Logical: logical, Check: CheckMagic, Tier: engine.TierWrongFormat, Passed: true,
	}}
	if !dec.HashValid {
		checks = append(checks, engine.CheckResult{
			Logical: logical, Check: CheckMagic, Tier: engine.TierWrongFormat, Passed: false,
			Reason: "stored checksum doesn't match the file's contents",
		})
	}
	verdict := engine.Verdict{Portable: true, Checks: checks}
	for _, c := range checks {
		if !c.Passed {
			verdict.Portable = false
			verdict.Tier = engine.TierWrongFormat
		}
	}
	return verdict
}

// slotIDOf reads the slot number a save carries internally - the
// trailing bytes of its decrypted body. Takes a plain body slice (rather
// than re.Decoded) so it works for both re.Decoded.Body and RE4's
// re.LimeDecoded.Body.
func slotIDOf(body []byte) (int32, bool) {
	if len(body) < 4 {
		return 0, false
	}
	tail := body[len(body)-4:]
	return int32(uint32(tail[0]) | uint32(tail[1])<<8 | uint32(tail[2])<<16 | uint32(tail[3])<<24), true
}

// expectedSlotID maps a slot token to the number a save for that slot
// carries internally.
func expectedSlotID(token string) (int32, bool) {
	if token == profileSlotToken {
		return -1, true
	}
	n, err := strconv.Atoi(strings.TrimSuffix(token, "Slot"))
	if err != nil {
		return 0, false
	}
	return int32(n), true
}

// checkSlot refuses a conversion whose source save belongs to a
// different slot than the container it would be written into. The slot
// number is baked into the save, so a mismatch produces a file the
// console will not accept. key must match data's own side (nil for an
// unencrypted-PS5 title's PS5 payload).
func checkSlot(cfg Config, image gameapi.SaveImage, data, key []byte) error {
	token, err := slotToken(cfg, image.SaveName)
	if err != nil {
		return err
	}
	want, ok := expectedSlotID(token)
	if !ok {
		return fmt.Errorf("slot %q isn't a recognised slot name", token)
	}
	dec, err := re.Decode(data, key)
	if err != nil {
		return err
	}
	got, ok := slotIDOf(dec.Body)
	if !ok {
		return fmt.Errorf("save is too short to carry a slot number")
	}
	if got != want {
		return fmt.Errorf("source save is for slot %d but the target container is slot %d (%q); a save must be written to its own slot",
			got, want, token)
	}
	return nil
}

func (Engine) ConvertToPS5(cfgAny any, images []gameapi.SaveImage, pcDir string, _ map[string][]byte, _ map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if len(images) != 1 {
		return gameapi.ConversionResult{}, fmt.Errorf("reengine handles exactly one save image, got %d", len(images))
	}
	image := images[0]
	if image.PCFile == "" {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: PC save file was not resolved", image.Logical)
	}
	if image.SaveName == "" {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: PS5 save slot was not resolved (pass --ps5-save-name)", image.Logical)
	}

	pcData, err := os.ReadFile(filepath.Join(pcDir, image.PCFile))
	if err != nil {
		return gameapi.ConversionResult{}, err
	}

	if cfg.Title == "re4" {
		return convertRE4ToPS5(cfg, image, pcData)
	}

	title := titles[cfg.Title]
	if err := checkSlot(cfg, image, pcData, title.Key); err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}
	converted, err := title.ConvertPCToPS5(pcData)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.PCFile, err)
	}

	return gameapi.ConversionResult{
		Outputs: map[string][]byte{image.SaveName: converted},
		Manifest: map[string]any{
			"pc_file":  image.PCFile,
			"ps5_save": image.SaveName,
			"payload":  image.Payload,
			"note":     "The save-select entry keeps the previous occupant's text (area, goal, character) until the game next saves to this slot: that text lives in the PS5's own save registration, not in this file. Writing it would mean touching sce_sys/, which breaks the container's registration.",
		},
	}, nil
}

func (Engine) ConvertFromPS5(cfgAny any, images []gameapi.SaveImage, ps5Payloads map[string][]byte, _ string, _ map[string]bool) (gameapi.ConversionResult, error) {
	cfg := cfgAny.(Config)
	if len(images) != 1 {
		return gameapi.ConversionResult{}, fmt.Errorf("reengine handles exactly one save image, got %d", len(images))
	}
	image := images[0]
	data, ok := ps5Payloads[image.Logical]
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("missing PS5 payload for %s", image.Logical)
	}

	if cfg.Title == "re4" {
		return convertRE4FromPS5(cfg, image, data)
	}

	// A PC save embeds the Steam account id, and the source (a PS5 save)
	// doesn't carry one - it has to come from the caller. The game
	// silently omits a save whose embedded id isn't the loading
	// account's from its load list, so getting this wrong looks like an
	// empty slot rather than an error (see TestCases.md). Checked before
	// touching data at all, same as RE4's equivalent check in
	// convertRE4FromPS5, so a missing --steam-id fails fast rather than
	// after a wasted decode.
	if image.SteamID == 0 {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: converting to this game's Steam save format needs the account that will load it - pass --steam-id", image.Logical)
	}

	title := titles[cfg.Title]
	ps5Key := title.Key
	if title.PS5Unencrypted {
		ps5Key = nil
	}
	if err := checkSlot(cfg, image, data, ps5Key); err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}

	converted, err := title.ConvertPS5ToPC(data, image.SteamID)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}

	token, err := slotToken(cfg, image.SaveName)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	outName, err := fileForSlot(token)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}

	return gameapi.ConversionResult{
		Outputs: map[string][]byte{outName: converted},
		Manifest: map[string]any{
			"ps5_save": image.SaveName,
			"pc_file":  outName,
			"note":     "The embedded Steam account id is the --steam-id passed for this run, normalized to the 32-bit account id. It must be the account that will load the save: the game silently omits a save belonging to another account from its load list.",
		},
		Warnings: []string{
			"PS5->PC conversion has never been verified on a real PC install - back up the PC save directory first.",
		},
	}, nil
}

// convertRE4ToPS5 and convertRE4FromPS5 handle RE4 specially: its Steam
// build uses a genuinely different cipher (Lime, keyed off the Steam
// account) than every other title this engine supports, so it needs its
// own slot check and conversion call rather than the TitleConfig path
// ConvertToPS5/ConvertFromPS5 otherwise use. Confirmed format-level
// (round trip against two real Steam saves - see docs/dev-res2.md) but
// not tested in-game, and RE4PlatformClass's mapping was found from a
// single sample per side unlike RE2/RE3's multi-sample confirmation.
func convertRE4ToPS5(cfg Config, image gameapi.SaveImage, pcData []byte) (gameapi.ConversionResult, error) {
	if image.SteamID == 0 {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: RE4's Steam save format is keyed off the account - pass --steam-id", image.Logical)
	}
	token, err := slotToken(cfg, image.SaveName)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}
	want, ok := expectedSlotID(token)
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: slot %q isn't a recognised slot name", image.Logical, token)
	}
	dec, err := re.LimeDecode(pcData, image.SteamID)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}
	got, ok := slotIDOf(dec.Body)
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: save is too short to carry a slot number", image.Logical)
	}
	if got != want {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: source save is for slot %d but the target container is slot %d (%q); a save must be written to its own slot",
			image.Logical, got, want, token)
	}

	converted, err := re.ConvertRE4PCToPS5(pcData, image.SteamID)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.PCFile, err)
	}

	return gameapi.ConversionResult{
		Outputs: map[string][]byte{image.SaveName: converted},
		Manifest: map[string]any{
			"pc_file":  image.PCFile,
			"ps5_save": image.SaveName,
			"payload":  image.Payload,
			"note":     "RE4's platform-identity field mapping was found from a single real sample per side (unlike RE2/RE3's multi-sample confirmation) and this conversion has not been tested in-game. The save-select entry also keeps the previous occupant's text until the game next saves to this slot.",
		},
		Warnings: []string{
			"RE4 support is newer and less-verified than RE2/RE3: format-level round trip confirmed against real saves, but not yet tested in-game.",
		},
	}, nil
}

func convertRE4FromPS5(cfg Config, image gameapi.SaveImage, ps5Data []byte) (gameapi.ConversionResult, error) {
	if image.SteamID == 0 {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: converting to RE4's Steam save format needs the target account - pass --steam-id", image.Logical)
	}
	token, err := slotToken(cfg, image.SaveName)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}
	want, ok := expectedSlotID(token)
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: slot %q isn't a recognised slot name", image.Logical, token)
	}
	dec, err := re.Decode(ps5Data, re.KeyRE4)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}
	got, ok := slotIDOf(dec.Body)
	if !ok {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: save is too short to carry a slot number", image.Logical)
	}
	if got != want {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: source save is for slot %d but the target container is slot %d (%q); a save must be written to its own slot",
			image.Logical, got, want, token)
	}

	converted, err := re.ConvertRE4PS5ToPC(ps5Data, image.SteamID)
	if err != nil {
		return gameapi.ConversionResult{}, fmt.Errorf("%s: %w", image.Logical, err)
	}

	outName, err := fileForSlot(token)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}

	return gameapi.ConversionResult{
		Outputs: map[string][]byte{outName: converted},
		Manifest: map[string]any{
			"ps5_save": image.SaveName,
			"pc_file":  outName,
			"note":     "PS5->PC has not been confirmed in-game. RE4's platform-identity field mapping was found from a single real sample per side.",
		},
		Warnings: []string{
			"RE4 PS5->PC conversion has never been verified on a real PC install - back up the PC save directory first.",
		},
	}, nil
}

// InstallOutputs writes converted files into pcDir, backing up anything
// it would overwrite. Output keys are already real filenames, so they're
// used directly rather than re-derived from cfg.
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
