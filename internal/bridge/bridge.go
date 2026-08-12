package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"savesyncpspc/internal/garlic"

	savesyncengine "github.com/DIYTechnologist/savesync-engine"
	"github.com/DIYTechnologist/savesync-engine/engine"
	"github.com/DIYTechnologist/savesync-engine/gameapi"
	"github.com/DIYTechnologist/savesync-engine/games"
	"github.com/DIYTechnologist/savesync-engine/util"
)

const ToolVersion = "0.2.0"

type Options struct {
	GarlicURL string
	Timeout   time.Duration
	PS5UID    string
	// PS5SaveName selects which Garlic save_name to target for a game
	// whose save slots aren't a fixed, config-known constant (e.g.
	// Baldur's Gate 3's sdimg_Save0001/sdimg_Save0002/...). Required
	// whenever the selected engine's Images() returns an image with
	// DynamicSaveName set; ignored otherwise (Clair's SaveName is always
	// config-known, so this is a no-op for it).
	PS5SaveName string
	// SteamID is the Steam account ID some engines' PC-side ciphers are
	// keyed off (e.g. RE4's "Lime": there is no fixed key at all, the
	// account ID derives it). Required whenever the selected engine
	// needs it (currently just reengine's "re4" title); ignored
	// otherwise. Threaded through via gameapi.SaveImage.SteamID rather
	// than the engine.Engine interface itself, the same way
	// PS5SaveName flows through resolveDynamicImages instead of a
	// dedicated parameter - avoids changing every engine's method
	// signature for a fact only one engine currently needs.
	SteamID    uint64
	GamesDir   string
	Game       string
	TitleID    string
	BackupRoot string
	PCDir      string
	OutputDir  string
	Force      bool
	Install    bool
	Apply      bool
	Yes        bool
	// Allow names portability-gate checks to bypass for this run (see
	// engine.CheckResult / the unreal engine's gate.go). Unknown tokens
	// are a hard error. For pc-to-ps5, any non-empty Allow requires Apply
	// && Yes - console writes must name each bypassed check individually.
	Allow []string
	// AllowAll bypasses every tier-2 check for this run. Rejected
	// outright for pc-to-ps5.
	AllowAll bool
	Log      func(string)
}

// BuildOverrides validates options.Allow against the engine's known check
// tokens and expands AllowAll, or returns an error naming any unknown
// token alongside the valid list.
func BuildOverrides(overrideTokens []string, allow []string, allowAll bool) (map[string]bool, error) {
	valid := map[string]bool{}
	for _, token := range overrideTokens {
		valid[token] = true
	}
	overrides := map[string]bool{}
	if allowAll {
		for token := range valid {
			overrides[token] = true
		}
	}
	var unknown []string
	for _, token := range allow {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if !valid[token] {
			unknown = append(unknown, token)
			continue
		}
		overrides[token] = true
	}
	if len(unknown) > 0 {
		validList := make([]string, 0, len(valid))
		for token := range valid {
			validList = append(validList, token)
		}
		sort.Strings(validList)
		return nil, fmt.Errorf("unknown --allow check(s): %s; valid checks are: %s", strings.Join(unknown, ", "), strings.Join(validList, ", "))
	}
	return overrides, nil
}

// warnUnusedOverrides loudly flags an --allow token that didn't bypass
// anything - a silent no-op override is the failure mode that wastes an
// afternoon, so this is a warning even though it's not an error.
func warnUnusedOverrides(log func(string), allow []string, fired []string) {
	firedSet := map[string]bool{}
	for _, check := range fired {
		firedSet[check] = true
	}
	for _, token := range allow {
		token = strings.TrimSpace(token)
		if token != "" && !firedSet[token] {
			log(fmt.Sprintf("Warning: --allow %s did not bypass anything - that check did not fail on this run.", token))
		}
	}
}

func (o Options) logger() func(string) {
	if o.Log != nil {
		return o.Log
	}
	return func(message string) { fmt.Println(message) }
}

// AtomicWrite writes data to path via a temp file + fsync + rename.
func AtomicWrite(path string, data []byte) error {
	return util.AtomicWrite(path, data)
}

// outputDirMarker is written into every output directory this tool
// produces. PrepareOutputDir uses it to tell "a directory we created on a
// previous run" apart from an arbitrary non-empty directory the user
// pointed --output-dir at, before deciding whether --force may delete it.
const outputDirMarker = "garlic_sync_manifest.json"

func PrepareOutputDir(outputDir string, force bool, protected []string) error {
	resolved, err := filepath.Abs(outputDir)
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	dangerous := map[string]bool{}
	for _, path := range []string{cwd, home} {
		if path != "" {
			if abs, err := filepath.Abs(path); err == nil {
				dangerous[abs] = true
			}
		}
	}
	for _, path := range protected {
		if abs, err := filepath.Abs(path); err == nil {
			dangerous[abs] = true
		}
	}
	if dangerous[resolved] {
		return fmt.Errorf("refusing to use dangerous output directory: %s", resolved)
	}
	if info, err := os.Stat(outputDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("output path exists and is not a directory: %s", outputDir)
		}
		if !force {
			return fmt.Errorf("output directory already exists: %s; use --force", outputDir)
		}
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			if _, err := os.Stat(filepath.Join(outputDir, outputDirMarker)); err != nil {
				return fmt.Errorf("refusing to remove non-empty output directory not created by a previous run (no %s found): %s", outputDirMarker, resolved)
			}
		}
		if err := os.RemoveAll(outputDir); err != nil {
			return err
		}
	}
	return os.MkdirAll(outputDir, 0o755)
}

// requirePCFiles controls what happens when an image's PCFile doesn't
// exist locally: true (the pc-to-ps5 direction, where the PC file is
// about to be read as the conversion source moments later - it must
// already be there) turns a missing file into an error here rather than
// a more confusing one downstream; false (ps5-to-pc) just skips that
// image's backup, since a first-time sync's PC-side output doesn't exist
// yet and there's nothing to back up - the engine creates it fresh (see
// e.g. unityblb, which needs no pre-existing PC template unlike Unreal's
// envelope-graft engines).
func BackupCurrentSaves(backupRoot, game, pcDir string, ps5Payloads map[string][]byte, saveImages []gameapi.SaveImage, now time.Time, requirePCFiles bool) (string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rootAbs, err := filepath.Abs(backupRoot)
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(rootAbs, fmt.Sprintf("%s-%s", game, now.Format("20060102150405")))
	if _, err := os.Stat(backupDir); err == nil {
		return "", fmt.Errorf("backup directory already exists: %s", backupDir)
	}
	pcBackup := filepath.Join(backupDir, "PC")
	if err := os.MkdirAll(pcBackup, 0o755); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, image := range saveImages {
		if image.PCFile == "" || seen[image.PCFile] {
			continue
		}
		seen[image.PCFile] = true
		source := filepath.Join(pcDir, image.PCFile)
		dest := filepath.Join(pcBackup, image.PCFile)
		info, err := os.Stat(source)
		if err != nil {
			if !requirePCFiles && os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("cannot create backup; missing PC save file: %s", source)
		}
		// Some engines' PC-side state is a whole directory tree (e.g.
		// Subnautica's per-slot save folder), not a single file.
		if info.IsDir() {
			if err := util.CopyDir(source, dest); err != nil {
				return "", err
			}
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return "", fmt.Errorf("cannot create backup; missing PC save file: %s", source)
		}
		if err := AtomicWrite(dest, data); err != nil {
			return "", err
		}
	}
	for _, image := range saveImages {
		data, ok := ps5Payloads[image.Logical]
		if !ok {
			return "", fmt.Errorf("missing PS5 payload for %s", image.Logical)
		}
		if err := AtomicWrite(filepath.Join(backupDir, "PS5", image.SaveName, image.Payload), data); err != nil {
			return "", err
		}
	}
	return backupDir, nil
}

// sanitizeGarlicPathComponent rejects a SaveName/Payload value that isn't
// safe to use as a single filesystem path component. image.Payload for a
// DynamicPayload image is chosen from filenames listed by Garlic's own
// mount-listing response (see resolveDynamicImages) - network input from
// the PS5 device, not something this tool generates itself - so a
// crafted or compromised response naming a file like "../../evil.bin"
// must not be allowed to escape the backup/output directory when later
// joined via filepath.Join. A legitimate name is always exactly one path
// component, so anything that doesn't round-trip through filepath.Base
// unchanged (or is empty) is rejected outright rather than silently
// cleaned.
func sanitizeGarlicPathComponent(kind, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty Garlic %s name", kind)
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("refusing unsafe Garlic %s name %q", kind, name)
	}
	return name, nil
}

// steamAccountID normalizes whatever the user passed to --steam-id into
// the 32-bit Steam *account* ID (the number in Steam's userdata/<id>/
// path), accepting either that or the 64-bit SteamID64.
//
// This distinction is not cosmetic. RE2/RE3 PC saves embed this value
// verbatim in the container's ID field, and real saves hold the account
// ID: writing a SteamID64 there instead produces a save the game treats
// as belonging to a different account and silently omits from its load
// list entirely - no error, just an apparently empty slot (confirmed
// live on RE3, 2026-08-06; see TestCases.md).
//
// A SteamID64 for an individual account is 0x1100001_00000000 + the
// account ID, so masking to the low 32 bits recovers the account ID and
// leaves an already-32-bit value untouched. RE4's Lime cipher applies
// this same mask internally (limeExponent), so normalizing here matches
// what that path already does rather than changing its behaviour.
func steamAccountID(steamID uint64) uint64 {
	return steamID & 0xffffffff
}

// resolveDynamicImages fills in any SaveName/Payload/PCFile fields the
// engine couldn't know ahead of time (see gameapi.SaveImage's Dynamic*
// fields), returning a fully-concrete copy of images. This runs before
// any backup or conversion step, since those need real filenames -
// there's no static config for a save whose slot and filenames are
// per-instance (e.g. Baldur's Gate 3).
//
// PCFile resolution (discovering an existing local file to read) only
// runs for direction "pc-to-ps5", where the PC side is the conversion
// source. For "ps5-to-pc" the PC-side file doesn't exist yet - it's an
// output the engine names itself (typically from the resolved Payload),
// not something to discover.
func resolveDynamicImages(client *garlic.Client, eng engine.Engine, cfg any, titleIDs []string, images []gameapi.SaveImage, pcDir, ps5UID, ps5SaveName string, steamID uint64, direction string) ([]gameapi.SaveImage, error) {
	resolved := make([]gameapi.SaveImage, len(images))
	accountID := steamAccountID(steamID)
	for i, image := range images {
		image.SteamID = accountID
		if image.DynamicSaveName {
			if ps5SaveName == "" {
				return nil, fmt.Errorf("%s: this game's PS5 save slot isn't fixed; pass --ps5-save-name (check Garlic's save list for the sdimg_SaveNNNN you want)", image.Logical)
			}
			sanitized, err := sanitizeGarlicPathComponent("save name", ps5SaveName)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", image.Logical, err)
			}
			image.SaveName = sanitized
		}
		if image.DynamicPCFile && direction == "pc-to-ps5" {
			pcFile, err := eng.ResolvePCFile(cfg, image, pcDir)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", image.Logical, err)
			}
			image.PCFile = pcFile
		}
		if image.DynamicPayload {
			_, files, err := client.MountByName(titleIDs, image.SaveName, ps5UID)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", image.Logical, err)
			}
			names := make([]string, 0, len(files))
			for _, f := range files {
				if !f.Dir {
					names = append(names, f.Name)
				}
			}
			payload, resolveErr := eng.ResolvePayload(cfg, image, names)
			if unmountErr := client.Unmount(); unmountErr != nil && resolveErr == nil {
				resolveErr = fmt.Errorf("unmount: %w", unmountErr)
			}
			if resolveErr != nil {
				return nil, fmt.Errorf("%s: %w", image.Logical, resolveErr)
			}
			sanitized, err := sanitizeGarlicPathComponent("payload", payload)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", image.Logical, err)
			}
			image.Payload = sanitized
		}
		resolved[i] = image
	}
	return resolved, nil
}

func PS5ToPC(options Options) error {
	log := options.logger()
	client := garlic.New(options.GarlicURL, options.Timeout)
	selected, err := selectGame(options, client)
	if err != nil {
		return err
	}
	overrides, err := BuildOverrides(selected.Engine.OverrideTokens(), options.Allow, options.AllowAll)
	if err != nil {
		return err
	}
	profile := selected.Profile
	if err := PrepareOutputDir(options.OutputDir, options.Force, []string{options.PCDir}); err != nil {
		return err
	}
	images, err := resolveDynamicImages(client, selected.Engine, selected.Config, profile.TitleIDs, selected.Engine.Images(selected.Config), options.PCDir, options.PS5UID, options.PS5SaveName, options.SteamID, "ps5-to-pc")
	if err != nil {
		return err
	}
	ps5Payloads := map[string][]byte{}
	for _, image := range images {
		log(fmt.Sprintf("Pulling %s %s/%s from Garlic...", profile.Name, image.SaveName, image.Payload))
		data, err := client.FetchPayload(profile.TitleIDs, image.SaveName, image.Payload, options.PS5UID)
		if err != nil {
			return err
		}
		ps5Payloads[image.Logical] = data
	}
	backupDir, err := BackupCurrentSaves(options.BackupRoot, profile.Key, options.PCDir, ps5Payloads, images, time.Time{}, false)
	if err != nil {
		return err
	}
	log("Backed up current PC and PS5 saves to: " + backupDir)
	result, err := selected.Engine.ConvertFromPS5(selected.Config, images, ps5Payloads, options.PCDir, overrides)
	if err != nil {
		return err
	}
	warnUnusedOverrides(log, options.Allow, result.OverriddenChecks)
	printWarnings(log, result.Warnings)
	for rel, data := range result.Outputs {
		if err := AtomicWrite(filepath.Join(options.OutputDir, rel), data); err != nil {
			return err
		}
	}
	manifest := map[string]any{
		"tool_version":      ToolVersion,
		"created":           time.Now().UTC().Format(time.RFC3339),
		"direction":         "ps5-to-pc-via-garlic",
		"game":              profile.Key,
		"game_name":         profile.Name,
		"title_ids":         profile.TitleIDs,
		"garlic":            options.GarlicURL,
		"ps5_uid":           options.PS5UID,
		"backup_dir":        backupDir,
		"overrides_allowed": options.Allow,
		"overrides_fired":   result.OverriddenChecks,
		"plugin":            result.Manifest,
	}
	if err := writeJSON(filepath.Join(options.OutputDir, "garlic_sync_manifest.json"), manifest); err != nil {
		return err
	}
	log("Created converted PC files in: " + options.OutputDir)
	if options.Install {
		if err := selected.Engine.InstallOutputs(selected.Config, result.Outputs, options.PCDir, filepath.Join(backupDir, "PC")); err != nil {
			return err
		}
		log("Installed into PC directory: " + options.PCDir)
		log("Previous PC files are backed up in: " + filepath.Join(backupDir, "PC"))
	}
	return nil
}

func PCToPS5(options Options) error {
	log := options.logger()
	client := garlic.New(options.GarlicURL, options.Timeout)
	selected, err := selectGame(options, client)
	if err != nil {
		return err
	}
	if options.AllowAll {
		return fmt.Errorf("--allow-all is not permitted for pc-to-ps5; name each bypassed check individually with --allow")
	}
	if len(options.Allow) > 0 && !(options.Apply && options.Yes) {
		return fmt.Errorf("bypassing a portability check for pc-to-ps5 requires --apply --yes")
	}
	overrides, err := BuildOverrides(selected.Engine.OverrideTokens(), options.Allow, false)
	if err != nil {
		return err
	}
	profile := selected.Profile
	if err := PrepareOutputDir(options.OutputDir, options.Force, []string{options.PCDir}); err != nil {
		return err
	}
	images, err := resolveDynamicImages(client, selected.Engine, selected.Config, profile.TitleIDs, selected.Engine.Images(selected.Config), options.PCDir, options.PS5UID, options.PS5SaveName, options.SteamID, "pc-to-ps5")
	if err != nil {
		return err
	}
	payloadBySaveName := map[string]string{}
	ps5Templates := map[string][]byte{}
	for _, image := range images {
		payloadBySaveName[image.SaveName] = image.Payload
		log(fmt.Sprintf("Pulling PS5 template %s %s/%s from Garlic...", profile.Name, image.SaveName, image.Payload))
		data, err := client.FetchPayload(profile.TitleIDs, image.SaveName, image.Payload, options.PS5UID)
		if err != nil {
			return err
		}
		ps5Templates[image.Logical] = data
	}
	backupDir, err := BackupCurrentSaves(options.BackupRoot, profile.Key, options.PCDir, ps5Templates, images, time.Time{}, true)
	if err != nil {
		return err
	}
	log("Backed up current PC and PS5 saves to: " + backupDir)
	result, err := selected.Engine.ConvertToPS5(selected.Config, images, options.PCDir, ps5Templates, overrides)
	if err != nil {
		return err
	}
	warnUnusedOverrides(log, options.Allow, result.OverriddenChecks)
	printWarnings(log, result.Warnings)
	for saveName, data := range result.Outputs {
		if err := AtomicWrite(filepath.Join(options.OutputDir, saveName, payloadBySaveName[saveName]), data); err != nil {
			return err
		}
	}
	manifest := map[string]any{
		"tool_version":      ToolVersion,
		"created":           time.Now().UTC().Format(time.RFC3339),
		"direction":         "pc-to-ps5-via-garlic",
		"game":              profile.Key,
		"game_name":         profile.Name,
		"title_ids":         profile.TitleIDs,
		"garlic":            options.GarlicURL,
		"ps5_uid":           options.PS5UID,
		"applied_to_ps5":    options.Apply,
		"backup_dir":        backupDir,
		"overrides_allowed": options.Allow,
		"overrides_fired":   result.OverriddenChecks,
		"plugin":            result.Manifest,
	}
	if err := writeJSON(filepath.Join(options.OutputDir, "garlic_sync_manifest.json"), manifest); err != nil {
		return err
	}
	log("Created PS5 replacement payloads in: " + options.OutputDir)
	if options.Apply {
		if !options.Yes {
			return fmt.Errorf("refusing to write to PS5 without --yes")
		}
		for saveName, data := range result.Outputs {
			payloadName := payloadBySaveName[saveName]
			log(fmt.Sprintf("Replacing %s %s/%s through Garlic...", profile.Name, saveName, payloadName))
			if err := client.ReplacePayload(profile.TitleIDs, saveName, payloadName, data, options.PS5UID); err != nil {
				return err
			}
		}
		log("Applied converted payloads to PS5. Start the game and verify the load menu.")
	} else {
		log("Dry run only. Re-run with --apply --yes to replace PS5 payloads.")
	}
	return nil
}

func selectGame(options Options, client *garlic.Client) (games.Selected, error) {
	var seen []string
	if options.Game == "" && options.TitleID == "" {
		saves, err := client.Saves()
		if err != nil {
			return games.Selected{}, err
		}
		for _, save := range saves {
			seen = append(seen, fmt.Sprint(save["title_id"]))
		}
	}
	return games.SelectProfile(options.GamesDir, options.Game, options.TitleID, seen, savesyncengine.Builtin)
}

func printWarnings(log func(string), warnings []string) {
	if len(warnings) == 0 {
		return
	}
	log("Compatibility warnings:")
	for _, warning := range warnings {
		log("  - " + warning)
	}
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, raw)
}

func SupportedGroups(gamesDir string, saves []garlic.Save) ([]map[string]any, error) {
	profiles, err := games.Profiles(gamesDir, savesyncengine.Builtin)
	if err != nil {
		return nil, err
	}
	var groups []map[string]any
	for _, profile := range profiles {
		eng, cfg, err := games.ResolveEngine(profile)
		if err != nil {
			continue
		}
		required := eng.Images(cfg)
		requiredNames := map[string]gameapi.SaveImage{}
		for _, image := range required {
			requiredNames[image.SaveName] = image
		}
		titleSet := map[string]bool{}
		for _, id := range profile.TitleIDs {
			titleSet[strings.ToUpper(id)] = true
		}
		buckets := map[string]map[string]garlic.Save{}
		for idx, save := range saves {
			if !titleSet[strings.ToUpper(fmt.Sprint(save["title_id"]))] {
				continue
			}
			if fmt.Sprint(save["type"]) != "ps5" || util.BoolValue(save["backup"], false) || util.BoolValue(save["usb"], false) {
				continue
			}
			saveName := fmt.Sprint(save["save_name"])
			if _, ok := requiredNames[saveName]; !ok {
				continue
			}
			save["idx"] = idx
			key := fmt.Sprint(save["uid"]) + "|" + strings.ToUpper(fmt.Sprint(save["title_id"]))
			if buckets[key] == nil {
				buckets[key] = map[string]garlic.Save{}
			}
			buckets[key][saveName] = save
		}
		for _, byName := range buckets {
			if len(byName) < len(required) {
				continue
			}
			first := byName[required[0].SaveName]
			var images []map[string]any
			complete := true
			for _, image := range required {
				save, ok := byName[image.SaveName]
				if !ok {
					complete = false
					break
				}
				images = append(images, map[string]any{
					"logical":   image.Logical,
					"label":     image.Label,
					"save_name": image.SaveName,
					"idx":       save["idx"],
				})
			}
			if !complete {
				continue
			}
			uid := fmt.Sprint(first["uid"])
			titleID := strings.ToUpper(fmt.Sprint(first["title_id"]))
			groups = append(groups, map[string]any{
				"key":        profile.Key + "|" + uid + "|" + titleID,
				"game":       profile.Key,
				"game_name":  profile.Name,
				"title_id":   titleID,
				"title_name": first["title_name"],
				"uid":        uid,
				"complete":   true,
				"images":     images,
			})
		}
	}
	return groups, nil
}
