// Package ludusavi looks up PC save locations from ludusavi-manifest
// (https://github.com/mtkennerly/ludusavi-manifest), a large,
// crowd-sourced YAML manifest of save-file locations for thousands of
// games (built from PCGamingWiki data). It's fetched over HTTP and cached
// on disk, since the manifest is tens of megabytes and changes
// infrequently - callers control when a fetch happens (there's no
// automatic background refresh), so this only touches the network when
// something actually asks for a lookup.
//
// This is a convenience data source layered on top of - not a
// replacement for - a game's own manually-verified pc_save_dirs/
// steam_app_id in games/<key>.json (see internal/pcpath): those stay
// authoritative when present, and a ludusavi lookup failure (offline, a
// game not present in the manifest, an unresolvable path template) never
// fails the overall suggestion request, it just contributes nothing.
package ludusavi

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"savesyncpspc/internal/util"
)

// ManifestURL is ludusavi-manifest's generated, always-current manifest.
// A var (not const) so tests can point it at an unreachable address to
// exercise the "fetch failed, fall back to stale cache" path without
// touching the real network.
var ManifestURL = "https://raw.githubusercontent.com/mtkennerly/ludusavi-manifest/master/data/manifest.yaml"

// Candidate is one save-directory guess derived from a manifest file
// entry. Path is always a directory (a trailing glob/wildcard path
// component, e.g. "*.sav", is stripped) - resolution also stops at the
// first placeholder this package can't resolve to a real value on this
// machine (e.g. <storeUserId>, a per-account ID this package has no way
// to know), leaving Path at the last fully-resolved ancestor directory
// instead of guessing further. Truncated is set in that case, so a
// caller/UI can indicate there's a further subfolder to pick manually.
type Candidate struct {
	Path      string
	Reason    string
	Exists    bool
	Truncated bool
}

type fileWhen struct {
	OS    string `yaml:"os"`
	Store string `yaml:"store"`
}

type fileEntry struct {
	Tags []string   `yaml:"tags"`
	When []fileWhen `yaml:"when"`
}

type gameEntry struct {
	Files map[string]fileEntry `yaml:"files"`
	Steam struct {
		ID int `yaml:"id"`
	} `yaml:"steam"`
}

// Manifest is a parsed ludusavi-manifest, decoded lazily per game: the
// top level is kept as raw yaml.Node values so looking up one game out of
// the manifest's several thousand doesn't pay to fully decode the rest.
type Manifest struct {
	games map[string]yaml.Node
}

// Load fetches (or reuses a cached copy of) the manifest and parses its
// top level. cachePath is where the raw YAML is cached; a cache younger
// than maxAge is reused without touching the network at all. If the
// fetch itself fails (offline, DNS, non-200), a stale cache is used
// instead of failing outright - only an unreadable manifest with no
// usable cache at all returns an error.
func Load(cachePath string, maxAge, timeout time.Duration) (*Manifest, error) {
	data, err := readOrFetch(cachePath, maxAge, timeout)
	if err != nil {
		return nil, err
	}
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing ludusavi manifest: %w", err)
	}
	return &Manifest{games: raw}, nil
}

// DefaultCachePath is where Load's cachePath argument should usually
// point: under the OS's standard cache directory, falling back to the
// system temp directory if that's unavailable.
func DefaultCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "savesyncpspc", "ludusavi-manifest.yaml")
}

func readOrFetch(cachePath string, maxAge, timeout time.Duration) ([]byte, error) {
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < maxAge {
		if data, err := os.ReadFile(cachePath); err == nil {
			return data, nil
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(ManifestURL)
	if err != nil {
		if data, readErr := os.ReadFile(cachePath); readErr == nil {
			return data, nil
		}
		return nil, fmt.Errorf("fetching ludusavi manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if data, readErr := os.ReadFile(cachePath); readErr == nil {
			return data, nil
		}
		return nil, fmt.Errorf("fetching ludusavi manifest: unexpected status %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if data, readErr := os.ReadFile(cachePath); readErr == nil {
			return data, nil
		}
		return nil, fmt.Errorf("reading ludusavi manifest response: %w", err)
	}
	// Best-effort cache write - a failure here (e.g. read-only cache dir)
	// shouldn't fail the lookup that just successfully fetched real data.
	_ = util.AtomicWrite(cachePath, data)
	return data, nil
}

// Suggest returns save-directory candidates for gameName (matched exactly
// against the manifest's top-level game names, e.g. "Baldur's Gate 3"),
// evaluated for the OS this process is actually running on (goos, i.e.
// runtime.GOOS - passed in rather than read directly so this stays
// testable against every OS's branch from any machine). A game absent
// from the manifest, or with no directory-shaped "save"-tagged file
// entries for this OS, returns an empty (non-error) slice.
func (m *Manifest) Suggest(gameName, goos string) []Candidate {
	node, ok := m.games[gameName]
	if !ok {
		return nil
	}
	var game gameEntry
	if err := node.Decode(&game); err != nil {
		return nil
	}

	ludusaviOS := goosToLudusavi(goos)
	var out []Candidate
	seen := map[string]bool{}
	for template, file := range game.Files {
		if !hasSaveTag(file.Tags) || !matchesOS(file.When, ludusaviOS) {
			continue
		}
		path, truncated, ok := resolveTemplate(template)
		if !ok || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		reason := "ludusavi: " + goos + " save location"
		if truncated {
			reason += " (partial - a further subfolder must be chosen manually)"
		}
		out = append(out, Candidate{Path: path, Reason: reason, Exists: dirExists(path), Truncated: truncated})
	}
	return out
}

// SteamAppID returns the manifest's Steam app ID for gameName, if known.
func (m *Manifest) SteamAppID(gameName string) (string, bool) {
	node, ok := m.games[gameName]
	if !ok {
		return "", false
	}
	var game gameEntry
	if err := node.Decode(&game); err != nil || game.Steam.ID == 0 {
		return "", false
	}
	return fmt.Sprint(game.Steam.ID), true
}

func goosToLudusavi(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

func hasSaveTag(tags []string) bool {
	for _, t := range tags {
		if t == "save" {
			return true
		}
	}
	return false
}

// matchesOS reports whether at least one "when" alternative matches this
// OS. A file with no "when" at all applies unconditionally. Entries
// scoped to the Microsoft Store are skipped even on a matching OS - their
// paths live in an opaque per-app sandboxed container
// (LocalAppData\Packages\...) that isn't a plain browsable directory the
// rest of this tool's model assumes.
func matchesOS(whens []fileWhen, ludusaviOS string) bool {
	if len(whens) == 0 {
		return true
	}
	for _, w := range whens {
		if w.Store == "microsoft" {
			continue
		}
		if w.OS == "" || w.OS == ludusaviOS {
			return true
		}
	}
	return false
}

// winEnvVars are placeholders resolved directly from a same-named
// Windows environment variable.
var winEnvVars = map[string]string{
	"winAppData":      "APPDATA",
	"winLocalAppData": "LOCALAPPDATA",
	"winPublic":       "PUBLIC",
	"winProgramData":  "PROGRAMDATA",
	"winDir":          "WINDIR",
}

// xdgVars are placeholders resolved from an XDG base-directory
// environment variable, falling back to its documented default under the
// home directory when unset.
var xdgVars = map[string]struct{ env, fallback string }{
	"xdgData":   {"XDG_DATA_HOME", ".local/share"},
	"xdgConfig": {"XDG_CONFIG_HOME", ".config"},
	"xdgCache":  {"XDG_CACHE_HOME", ".cache"},
}

// resolveSegment resolves one <placeholder> token to a real path
// component on this machine, or reports ok=false for placeholders this
// package doesn't have enough information to resolve (a per-account ID,
// a Steam library root, a game's own install directory, etc.).
func resolveSegment(token string) (string, bool) {
	switch token {
	case "home":
		home, err := os.UserHomeDir()
		return home, err == nil && home != ""
	case "winDocuments":
		profile := os.Getenv("USERPROFILE")
		if profile == "" {
			return "", false
		}
		return filepath.Join(profile, "Documents"), true
	}
	if env, ok := winEnvVars[token]; ok {
		value := os.Getenv(env)
		return value, value != ""
	}
	if xdg, ok := xdgVars[token]; ok {
		if value := os.Getenv(xdg.env); value != "" {
			return value, true
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(home, xdg.fallback), true
	}
	return "", false
}

// resolveTemplate walks a manifest path template ("/"-separated
// regardless of OS) segment by segment, resolving <placeholder> tokens
// and stopping at the first one it can't resolve or that's a glob (e.g.
// "*.sav") - the result is the last fully-resolved ancestor directory,
// with truncated=true if anything had to be cut off. ok is false only
// when nothing at all could be resolved (e.g. the template starts with an
// unresolvable placeholder).
func resolveTemplate(template string) (path string, truncated bool, ok bool) {
	segments := strings.Split(template, "/")
	var resolved []string
	for _, seg := range segments {
		if strings.ContainsAny(seg, "*?") {
			truncated = true
			break
		}
		if strings.HasPrefix(seg, "<") && strings.HasSuffix(seg, ">") {
			value, resolvedOK := resolveSegment(seg[1 : len(seg)-1])
			if !resolvedOK {
				truncated = true
				break
			}
			resolved = append(resolved, value)
			continue
		}
		resolved = append(resolved, seg)
	}
	if len(resolved) == 0 {
		return "", truncated, false
	}
	return filepath.Join(resolved...), truncated, true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
