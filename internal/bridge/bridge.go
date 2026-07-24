package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/games"
	"savesyncpspc/internal/garlic"
	"savesyncpspc/internal/util"
)

const ToolVersion = "0.2.0"

type Options struct {
	GarlicURL  string
	Timeout    time.Duration
	PS5UID     string
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
	Log        func(string)
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

func BackupCurrentSaves(backupRoot, game, pcDir, payloadName string, ps5Payloads map[string][]byte, saveImages []gameapi.SaveImage, now time.Time) (string, error) {
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
		data, err := os.ReadFile(source)
		if err != nil {
			return "", fmt.Errorf("cannot create backup; missing PC save file: %s", source)
		}
		if err := AtomicWrite(filepath.Join(pcBackup, image.PCFile), data); err != nil {
			return "", err
		}
	}
	for _, image := range saveImages {
		data, ok := ps5Payloads[image.Logical]
		if !ok {
			return "", fmt.Errorf("missing PS5 payload for %s", image.Logical)
		}
		if err := AtomicWrite(filepath.Join(backupDir, "PS5", image.SaveName, payloadName), data); err != nil {
			return "", err
		}
	}
	return backupDir, nil
}

func PS5ToPC(options Options) error {
	log := options.logger()
	client := garlic.New(options.GarlicURL, options.Timeout)
	profile, game, err := selectGame(options, client)
	if err != nil {
		return err
	}
	if err := PrepareOutputDir(options.OutputDir, options.Force, []string{options.PCDir}); err != nil {
		return err
	}
	payloadName := game.PayloadName()
	ps5Payloads := map[string][]byte{}
	for _, image := range game.SaveImages() {
		log(fmt.Sprintf("Pulling %s %s/%s from Garlic...", profile.Name, image.SaveName, payloadName))
		data, err := client.FetchPayload(profile.TitleIDs, image.SaveName, payloadName, options.PS5UID)
		if err != nil {
			return err
		}
		ps5Payloads[image.Logical] = data
	}
	backupDir, err := BackupCurrentSaves(options.BackupRoot, profile.Key, options.PCDir, payloadName, ps5Payloads, game.SaveImages(), time.Time{})
	if err != nil {
		return err
	}
	log("Backed up current PC and PS5 saves to: " + backupDir)
	result, err := game.ConvertFromPS5(ps5Payloads, options.PCDir)
	if err != nil {
		return err
	}
	printWarnings(log, result.Warnings)
	for rel, data := range result.Outputs {
		if err := AtomicWrite(filepath.Join(options.OutputDir, rel), data); err != nil {
			return err
		}
	}
	manifest := map[string]any{
		"tool_version": ToolVersion,
		"created":      time.Now().UTC().Format(time.RFC3339),
		"direction":    "ps5-to-pc-via-garlic",
		"game":         profile.Key,
		"game_name":    profile.Name,
		"title_ids":    profile.TitleIDs,
		"garlic":       options.GarlicURL,
		"ps5_uid":      options.PS5UID,
		"backup_dir":   backupDir,
		"plugin":       result.Manifest,
	}
	if err := writeJSON(filepath.Join(options.OutputDir, "garlic_sync_manifest.json"), manifest); err != nil {
		return err
	}
	log("Created converted PC files in: " + options.OutputDir)
	if options.Install {
		if err := game.InstallOutputs(result.Outputs, options.PCDir, filepath.Join(backupDir, "PC")); err != nil {
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
	profile, game, err := selectGame(options, client)
	if err != nil {
		return err
	}
	if err := PrepareOutputDir(options.OutputDir, options.Force, []string{options.PCDir}); err != nil {
		return err
	}
	payloadName := game.PayloadName()
	ps5Templates := map[string][]byte{}
	for _, image := range game.SaveImages() {
		log(fmt.Sprintf("Pulling PS5 template %s %s/%s from Garlic...", profile.Name, image.SaveName, payloadName))
		data, err := client.FetchPayload(profile.TitleIDs, image.SaveName, payloadName, options.PS5UID)
		if err != nil {
			return err
		}
		ps5Templates[image.Logical] = data
	}
	backupDir, err := BackupCurrentSaves(options.BackupRoot, profile.Key, options.PCDir, payloadName, ps5Templates, game.SaveImages(), time.Time{})
	if err != nil {
		return err
	}
	log("Backed up current PC and PS5 saves to: " + backupDir)
	result, err := game.ConvertToPS5(options.PCDir, ps5Templates)
	if err != nil {
		return err
	}
	printWarnings(log, result.Warnings)
	for saveName, data := range result.Outputs {
		if err := AtomicWrite(filepath.Join(options.OutputDir, saveName, payloadName), data); err != nil {
			return err
		}
	}
	manifest := map[string]any{
		"tool_version":   ToolVersion,
		"created":        time.Now().UTC().Format(time.RFC3339),
		"direction":      "pc-to-ps5-via-garlic",
		"game":           profile.Key,
		"game_name":      profile.Name,
		"title_ids":      profile.TitleIDs,
		"garlic":         options.GarlicURL,
		"ps5_uid":        options.PS5UID,
		"applied_to_ps5": options.Apply,
		"backup_dir":     backupDir,
		"plugin":         result.Manifest,
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

func selectGame(options Options, client *garlic.Client) (gameapi.Profile, gameapi.Game, error) {
	var seen []string
	if options.Game == "" && options.TitleID == "" {
		saves, err := client.Saves()
		if err != nil {
			return gameapi.Profile{}, nil, err
		}
		for _, save := range saves {
			seen = append(seen, fmt.Sprint(save["title_id"]))
		}
	}
	return games.SelectProfile(options.GamesDir, options.Game, options.TitleID, seen)
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
	profiles, err := games.Profiles(gamesDir)
	if err != nil {
		return nil, err
	}
	var groups []map[string]any
	for _, profile := range profiles {
		game, ok := games.Registered(profile.Key)
		if !ok {
			continue
		}
		required := game.SaveImages()
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
