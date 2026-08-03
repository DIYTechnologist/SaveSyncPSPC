// Package pcpath suggests where a game's PC saves probably live, so the
// UI/CLI can offer a starting point to browse from instead of requiring
// the user to already know the exact path. It assumes it's running on the
// same machine as the PC save files - the same assumption internal/ui's
// PC-browsing endpoint makes today, and the one a future standalone PC
// client (see docs/dev.md's "PC save directory discovery" notes) would
// remove by answering this query remotely instead of via runtime.GOOS and
// local environment variables.
//
// Suggestions are a convenience, not a requirement: --pc-dir (or the UI's
// "PC save directory" field) always works regardless of whether a game
// declares pc_save_dirs/steam_app_id at all.
package pcpath

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"savesyncpspc/internal/ludusavi"
)

// Candidate is one suggested PC save directory.
type Candidate struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Exists bool   `json:"exists"`
	// Truncated means Path stops short of the actual save file(s) -
	// ludusavi's template named a further placeholder (typically a
	// per-account ID) or a glob this package has no way to resolve, so
	// Path is the last fully-resolved ancestor directory instead of a
	// guess. The user picks the rest by browsing.
	Truncated bool `json:"truncated"`
}

// Suggest returns candidate PC save directories for the OS this process
// is actually running on (runtime.GOOS) - a profile's entries for other
// operating systems are never evaluated, since expanding them against
// this machine's environment would be meaningless. pcSaveDirs is keyed by
// GOOS value ("windows", "linux", "darwin"); steamAppID is optional and
// only used to additionally guess Linux Steam Play (Proton) compatdata
// locations from the "windows" entry.
func Suggest(pcSaveDirs map[string]string, steamAppID string) []Candidate {
	var out []Candidate
	goos := runtime.GOOS

	if tmpl := pcSaveDirs[goos]; tmpl != "" {
		if p, err := expandTemplate(tmpl); err == nil {
			out = append(out, Candidate{Path: filepath.Clean(p), Reason: goos + " default", Exists: dirExists(p)})
		}
	}

	if goos == "linux" && steamAppID != "" {
		if suffix := localAppDataSuffix(pcSaveDirs["windows"]); suffix != "" {
			for _, base := range linuxSteamBases() {
				p := filepath.Join(base, "steamapps", "compatdata", steamAppID, "pfx", "drive_c", "users", "steamuser", "AppData", "Local", filepath.FromSlash(suffix))
				out = append(out, Candidate{Path: p, Reason: "Steam Play (Proton) compatdata - guessed, unverified", Exists: dirExists(p)})
			}
		}
	}

	return out
}

// SuggestWithManifest is Suggest, additionally merged with candidates
// pulled from a ludusavi-manifest lookup for gameName (see
// internal/ludusavi) - a large, crowd-sourced, frequently-updated data
// source that covers far more games/platforms than any one game's
// hand-authored pc_save_dirs ever will. manifest may be nil (e.g. the
// fetch/parse failed, likely offline) - callers should treat that as
// "no ludusavi data available this time", not an error: everything
// pcSaveDirs/steamAppID already provide is still returned. If the
// profile doesn't set steamAppID but ludusavi knows one for gameName,
// that's used instead so Steam Play (Proton) compatdata guessing (see
// Suggest) still works. Candidates are de-duplicated by cleaned path,
// preferring whichever source produced a given path first.
func SuggestWithManifest(pcSaveDirs map[string]string, steamAppID string, manifest *ludusavi.Manifest, gameName string) []Candidate {
	effectiveSteamID := steamAppID
	if effectiveSteamID == "" && manifest != nil && gameName != "" {
		if id, ok := manifest.SteamAppID(gameName); ok {
			effectiveSteamID = id
		}
	}
	out := Suggest(pcSaveDirs, effectiveSteamID)
	if manifest == nil || gameName == "" {
		return out
	}
	seen := map[string]bool{}
	for _, c := range out {
		seen[filepath.Clean(c.Path)] = true
	}
	for _, lc := range manifest.Suggest(gameName, runtime.GOOS) {
		p := filepath.Clean(lc.Path)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, Candidate{Path: p, Reason: lc.Reason, Exists: lc.Exists, Truncated: lc.Truncated})
	}
	return out
}

// expandTemplate resolves %LOCALAPPDATA%/%APPDATA%/%USERPROFILE% and a
// leading ~ against this process's actual environment. Returns an error
// if a placeholder the template used isn't set (e.g. evaluating a
// "windows" template's %LOCALAPPDATA% when not actually running on
// Windows) rather than silently producing a broken path.
func expandTemplate(tmpl string) (string, error) {
	out := tmpl
	for _, env := range []string{"LOCALAPPDATA", "APPDATA", "USERPROFILE"} {
		placeholder := "%" + env + "%"
		if !strings.Contains(out, placeholder) {
			continue
		}
		value := os.Getenv(env)
		if value == "" {
			return "", fmt.Errorf("%%%s%% is not set", env)
		}
		out = strings.ReplaceAll(out, placeholder, value)
	}
	if strings.HasPrefix(out, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		out = filepath.Join(home, strings.TrimPrefix(out, "~"))
	}
	return out, nil
}

// localAppDataSuffix strips a template's leading %LOCALAPPDATA%\ (or /)
// so the remainder can be re-rooted under a Proton prefix's own emulated
// AppData\Local on Linux. Returns "" if the template isn't of that shape.
func localAppDataSuffix(windowsTemplate string) string {
	const marker = "%LOCALAPPDATA%"
	idx := strings.Index(windowsTemplate, marker)
	if idx == -1 {
		return ""
	}
	rest := windowsTemplate[idx+len(marker):]
	rest = strings.ReplaceAll(rest, "\\", "/")
	rest = strings.TrimPrefix(rest, "/")
	return rest
}

// linuxSteamBases lists Steam library root guesses on Linux: the common
// native install, the classic ~/.steam/steam symlink, and Flatpak Steam's
// data directory. Not exhaustive (doesn't parse libraryfolders.vdf for
// additional drives) - a best-effort starting point, same spirit as the
// rest of this package.
func linuxSteamBases() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", "data", "Steam"),
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
