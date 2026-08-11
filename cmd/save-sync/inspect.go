package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"savesyncpspc"
	"savesyncpspc/internal/bridge"
	"savesyncpspc/internal/garlic"

	"savesync-engine/engine"
	"savesync-engine/engine/unreal"
	"savesync-engine/games"
	"savesync-engine/gvas"
)

// runInspectCommand implements `save-sync inspect`. It never writes
// anything - no output directory, no backup, no PS5 upload - which is
// what makes it safe to use as the primary workflow for authoring a new
// game's class_equivalence table (see docs/dev.md).
func runInspectCommand(args []string, garlicURL string, timeout time.Duration, ps5UID, gamesDir, gameKey, titleID string) error {
	sub := flag.NewFlagSet("inspect", flag.ContinueOnError)
	sub.SetOutput(os.Stderr)
	filePath := sub.String("file", "", "Inspect a single local save file offline, no Garlic connection")
	pcDir := sub.String("pc-dir", "", "PC save directory; enables full pairwise checks (class-map, package-version) against the pulled PS5 payload")
	record := sub.Bool("record", false, "Print a candidate class_equivalence row for any class-map miss")
	var allow stringListFlag
	sub.Var(&allow, "allow", "Bypass named portability checks (comma-separated or repeatable)")
	allowAll := sub.Bool("allow-all", false, "Bypass every tier-2 check")
	if err := sub.Parse(args); err != nil {
		return err
	}

	if *filePath != "" {
		return inspectFile(*filePath, gamesDir, gameKey, titleID, allow.values, *allowAll)
	}
	if garlicURL == "" {
		return fmt.Errorf("--garlic is required unless --file is given")
	}
	if gameKey == "" && titleID == "" {
		return fmt.Errorf("--game (or --title-id) is required")
	}
	return inspectGarlic(garlicURL, timeout, ps5UID, gamesDir, gameKey, titleID, *pcDir, allow.values, *allowAll, *record)
}

// inspectFile runs only the single-payload checks (there's no "other
// side" to compare against) offline, against one local file.
func inspectFile(path, gamesDir, gameKey, titleID string, allow []string, allowAll bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	eng, cfg, err := resolveEngineForInspect(gamesDir, gameKey, titleID)
	if err != nil {
		return err
	}
	overrides, err := bridge.BuildOverrides(eng.OverrideTokens(), allow, allowAll)
	if err != nil {
		return err
	}
	label := filepath.Base(path)
	verdict := eng.Inspect(cfg, label, data, engine.SidePC, overrides)
	printVerdict(label+" (single-payload checks only; pass --game and use the --garlic form with --pc-dir for pairwise checks)", verdict.Checks)
	if !verdict.Portable {
		return fmt.Errorf("blocked (tier %d)", verdict.Tier)
	}
	fmt.Println("Portable.")
	return nil
}

func resolveEngineForInspect(gamesDir, gameKey, titleID string) (engine.Engine, any, error) {
	if gameKey == "" && titleID == "" {
		return unreal.New(), nil, nil
	}
	selected, err := games.SelectProfile(gamesDir, gameKey, titleID, nil, savesyncpspc.Builtin)
	if err != nil {
		return nil, nil, err
	}
	return selected.Engine, selected.Config, nil
}

// inspectGarlic pulls each required PS5 save image read-only (mount +
// download + unmount, same as a real conversion, minus the conversion)
// and, if --pc-dir was given, runs the full pairwise gate against the
// corresponding PC file.
func inspectGarlic(garlicURL string, timeout time.Duration, ps5UID, gamesDir, gameKey, titleID, pcDir string, allow []string, allowAll, record bool) error {
	client := garlic.New(garlicURL, timeout)
	selected, err := games.SelectProfile(gamesDir, gameKey, titleID, nil, savesyncpspc.Builtin)
	if err != nil {
		return err
	}
	overrides, err := bridge.BuildOverrides(selected.Engine.OverrideTokens(), allow, allowAll)
	if err != nil {
		return err
	}
	images := selected.Engine.Images(selected.Config)
	unrealEng, isUnreal := selected.Engine.(unreal.Engine)

	allPortable := true
	for _, image := range images {
		if image.DynamicSaveName || image.DynamicPayload {
			return fmt.Errorf("%s: `inspect` doesn't support engines with dynamic save images yet (engine %q); use ps5-to-pc/pc-to-ps5 with --ps5-save-name instead", image.Logical, selected.Engine.Name())
		}
		ps5Data, err := client.FetchPayload(selected.Profile.TitleIDs, image.SaveName, image.Payload, ps5UID)
		if err != nil {
			return fmt.Errorf("%s: %w", image.Logical, err)
		}

		if pcDir == "" {
			verdict := selected.Engine.Inspect(selected.Config, image.Logical, ps5Data, engine.SidePS5, overrides)
			printVerdict(image.Logical+" (PS5 payload only; pass --pc-dir for pairwise checks)", verdict.Checks)
			if !verdict.Portable {
				allPortable = false
			}
			continue
		}

		pcData, err := os.ReadFile(filepath.Join(pcDir, image.PCFile))
		if err != nil {
			return err
		}
		if !isUnreal {
			return fmt.Errorf("pairwise inspection isn't available yet for engine %q", selected.Engine.Name())
		}
		checks, err := unrealEng.GateChecks(selected.Config.(unreal.Config), image.Logical, ps5Data, pcData, engine.SidePS5, engine.SidePC, "ps5-to-pc", overrides)
		if err != nil {
			return fmt.Errorf("%s: %w", image.Logical, err)
		}
		printVerdict(image.Logical, checks)
		for _, c := range checks {
			if !c.Passed && !c.Overridden {
				allPortable = false
			}
			if record && c.Check == unreal.CheckClassMap && !c.Passed {
				sourceInfo, _ := gvas.Parse(ps5Data, "ps5")
				targetInfo, _ := gvas.Parse(pcData, "pc")
				printCandidateRow(image.Logical, targetInfo.SaveClass, sourceInfo.SaveClass)
			}
		}
	}
	if !allPortable {
		return fmt.Errorf("one or more images were blocked; re-run with --allow <check> to bypass a specific one")
	}
	fmt.Println("All images portable.")
	return nil
}

func printVerdict(label string, checks []engine.CheckResult) {
	fmt.Printf("%s:\n", label)
	for _, c := range checks {
		status := "PASS"
		switch {
		case c.Overridden:
			status = "OVERRIDDEN"
		case c.Warn:
			status = "CANDIDATE"
		case !c.Passed:
			status = "BLOCKED"
		}
		line := fmt.Sprintf("  [%s] %s", status, c.Check)
		if c.Reason != "" {
			line += " - " + c.Reason
		}
		fmt.Println(line)
	}
}

func printCandidateRow(logical, pcClass, ps5Class string) {
	fmt.Printf(`
Candidate row for games/<key>.json's class_equivalence (%s):
  {
    "logical": %q,
    "pc": %q,
    "ps5": %q,
    "verified": false
  }

`, logical, logical, pcClass, ps5Class)
}
