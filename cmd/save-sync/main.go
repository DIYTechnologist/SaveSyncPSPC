package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"savesyncpspc/internal/bridge"

	"savesync-engine/games"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(version)
		return nil
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printUsage()
		return nil
	}
	commandIndex := -1
	for i, arg := range args {
		switch arg {
		case "ps5-to-pc", "ps5-to-steam", "pc-to-ps5", "steam-to-ps5", "inspect":
			commandIndex = i
		}
	}
	if commandIndex < 0 {
		return fmt.Errorf("missing command: ps5-to-pc, pc-to-ps5, or inspect")
	}

	global := flag.NewFlagSet("save-sync", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	garlicURL := global.String("garlic", "", "Garlic base URL, e.g. http://192.168.1.50:8082")
	timeoutSeconds := global.Float64("timeout", 120, "HTTP timeout in seconds")
	ps5UID := global.String("ps5-uid", "", "Optional PS5 user id filter from Garlic /api/saves")
	ps5SaveName := global.String("ps5-save-name", "", "Garlic save_name to target for games whose save slots aren't fixed (e.g. Baldur's Gate 3's sdimg_SaveNNNN)")
	steamID := global.String("steam-id", "", "Steam account ID of the account that will load the save - the number in Steam's userdata/<id>/ path, or the SteamID64 (either is accepted). Needed by games that bind a PC save to an account (RE2/RE3 embed it; RE4's Lime cipher is keyed off it)")
	gamesDir := global.String("games-dir", games.DefaultGamesDir(), "Game metadata directory")
	gameKey := global.String("game", "", "Game key from games/<game>.json, e.g. clair")
	titleID := global.String("title-id", "", "PS5 title id to map through games/*.json, e.g. PPSA17599")
	backupRoot := global.String("backup-root", "backup", "Backup root directory")
	if err := global.Parse(args[:commandIndex]); err != nil {
		return err
	}

	command := args[commandIndex]
	timeout := time.Duration(*timeoutSeconds * float64(time.Second))

	if command == "inspect" {
		return runInspectCommand(args[commandIndex+1:], *garlicURL, timeout, *ps5UID, *gamesDir, *gameKey, *titleID)
	}

	if *garlicURL == "" {
		return fmt.Errorf("--garlic is required")
	}

	sub := flag.NewFlagSet(command, flag.ContinueOnError)
	sub.SetOutput(os.Stderr)
	pcDir := sub.String("pc-dir", "", "PC save directory")
	steamDir := sub.String("steam-dir", "", "PC save directory")
	outputDir := sub.String("output-dir", defaultOutputDir(command), "Output directory")
	force := sub.Bool("force", false, "Replace existing output directory")
	install := sub.Bool("install", false, "Back up and replace files in --pc-dir")
	apply := sub.Bool("apply", false, "Replace PS5 payloads through Garlic")
	yes := sub.Bool("yes", false, "Confirm --apply writes to PS5")
	var allow stringListFlag
	sub.Var(&allow, "allow", "Bypass named portability checks (comma-separated or repeatable); see `save-sync inspect`")
	allowAll := sub.Bool("allow-all", false, "Bypass every tier-2 portability check (rejected for pc-to-ps5)")
	if err := sub.Parse(args[commandIndex+1:]); err != nil {
		return err
	}
	if *pcDir != "" && *steamDir != "" && *pcDir != *steamDir {
		fmt.Fprintln(os.Stderr, "Warning: both --pc-dir and --steam-dir were set; using --pc-dir and ignoring --steam-dir")
	}
	if *pcDir == "" {
		*pcDir = *steamDir
	}
	if *pcDir == "" {
		return fmt.Errorf("--pc-dir is required")
	}

	var steamIDValue uint64
	if *steamID != "" {
		parsed, err := strconv.ParseUint(*steamID, 10, 64)
		if err != nil {
			return fmt.Errorf("--steam-id must be a number: %w", err)
		}
		steamIDValue = parsed
	}

	options := bridge.Options{
		GarlicURL:   *garlicURL,
		Timeout:     timeout,
		PS5UID:      *ps5UID,
		PS5SaveName: *ps5SaveName,
		SteamID:     steamIDValue,
		GamesDir:    *gamesDir,
		Game:        *gameKey,
		TitleID:     *titleID,
		BackupRoot:  *backupRoot,
		PCDir:       *pcDir,
		OutputDir:   *outputDir,
		Force:       *force,
		Install:     *install,
		Apply:       *apply,
		Yes:         *yes,
		Allow:       allow.values,
		AllowAll:    *allowAll,
	}
	switch command {
	case "ps5-to-pc", "ps5-to-steam":
		return bridge.PS5ToPC(options)
	case "pc-to-ps5", "steam-to-ps5":
		return bridge.PCToPS5(options)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

// stringListFlag accumulates --flag values across multiple occurrences and
// splits each occurrence on commas, so both `--allow a --allow b` and
// `--allow a,b` work.
type stringListFlag struct {
	values []string
}

func (f *stringListFlag) String() string { return strings.Join(f.values, ",") }

func (f *stringListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			f.values = append(f.values, part)
		}
	}
	return nil
}

func printUsage() {
	fmt.Printf(`Save Sync PS-PC %s

Usage:
  save-sync [global options] ps5-to-pc [options]
  save-sync [global options] pc-to-ps5 [options]
  save-sync [global options] inspect [options]

Global options:
  --garlic URL          Garlic base URL, e.g. http://192.168.1.50:8082
  --timeout SECONDS     HTTP timeout in seconds
  --ps5-uid UID         Optional PS5 user id filter from Garlic
  --ps5-save-name NAME  Garlic save_name to target for games without a fixed
                        save slot (e.g. Baldur's Gate 3's sdimg_SaveNNNN)
  --steam-id ID         Steam account ID of the account that will load the
                        save: the number in Steam's userdata/<id>/ path, or
                        the SteamID64 (either accepted). Needed by games that
                        bind a PC save to an account - RE2/RE3 embed it in the
                        save, RE4's Lime cipher is keyed off it
  --games-dir DIR       Game metadata directory
  --game KEY            Game key, e.g. clair
  --title-id ID         PS5 title id, e.g. PPSA17599
  --backup-root DIR     Backup root directory

Command options:
  --pc-dir DIR          PC save directory
  --output-dir DIR      Output directory
  --force               Replace existing output directory
  --install             Install ps5-to-pc outputs into --pc-dir
  --apply --yes         Apply pc-to-ps5 payloads through Garlic
  --allow CHECK[,...]   Bypass named portability checks for this run (repeatable)
  --allow-all           Bypass every tier-2 check (rejected for pc-to-ps5)

Inspect options (writes nothing, no Garlic writes either):
  save-sync --garlic URL --game KEY inspect [--pc-dir DIR] [--ps5-uid UID] [--record]
  save-sync inspect --file PATH [--game KEY]
    --pc-dir DIR        Enables full pairwise checks (class-map, package-version)
    --record            Print a candidate class_equivalence row for a class-map miss
    --file PATH         Inspect one local save file offline, no Garlic connection
`, version)
}

func defaultOutputDir(command string) string {
	switch command {
	case "ps5-to-pc", "ps5-to-steam":
		return "garlic_ps5_to_pc"
	default:
		return "garlic_pc_to_ps5"
	}
}
