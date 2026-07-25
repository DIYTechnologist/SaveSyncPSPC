package games

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"savesyncpspc"
	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/engine/larian"
	"savesyncpspc/internal/engine/unreal"
	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/util"
)

// DefaultGamesDir is the on-disk override/extension directory checked in
// addition to the metadata embedded in the binary (see Builtin in the
// root package). It doesn't need to exist; a missing or empty directory
// here is not an error as long as at least one game's metadata resolves.
func DefaultGamesDir() string {
	return "games"
}

func init() {
	engine.Register(unreal.New())
	engine.Register(larian.New())
}

type metadata struct {
	Game         string          `json:"game"`
	Name         string          `json:"name"`
	ID           string          `json:"id"`
	IDs          json.RawMessage `json:"ids"`
	Engine       string          `json:"engine"`
	EngineConfig json.RawMessage `json:"engine_config"`
}

type metadataID struct {
	ID string `json:"id"`
}

// Selected bundles a resolved game profile with the engine implementation
// and already-parsed engine_config that will do its conversion work.
type Selected struct {
	Profile gameapi.Profile
	Engine  engine.Engine
	Config  any
}

// ResolveEngine looks up profile's declared engine by name and parses its
// engine_config against that engine. Returns an error naming the profile's
// key if the engine is unknown or the config fails to parse, so a broken
// or not-yet-implemented profile can be skipped by callers that enumerate
// all profiles (e.g. SupportedGroups) rather than failing the whole batch.
func ResolveEngine(profile gameapi.Profile) (engine.Engine, any, error) {
	eng, ok := engine.Get(profile.Engine)
	if !ok {
		return nil, nil, fmt.Errorf("game %q uses unknown engine %q", profile.Key, profile.Engine)
	}
	cfg, err := eng.ParseConfig(profile.EngineConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("game %q: %w", profile.Key, err)
	}
	return eng, cfg, nil
}

func resolve(profile gameapi.Profile) (Selected, error) {
	eng, cfg, err := ResolveEngine(profile)
	if err != nil {
		return Selected{}, err
	}
	return Selected{Profile: profile, Engine: eng, Config: cfg}, nil
}

// Profiles loads game metadata, merging the metadata embedded in the
// binary with whatever *.json files are found under gamesDir. On-disk
// files take precedence over an embedded file with the same game key,
// so editing a materialized games/<game>.json next to the binary
// overrides the built-in default. The first time gamesDir doesn't exist,
// it's created and seeded from the embedded defaults (best effort - see
// materializeBuiltinGamesDir); an absent or empty on-disk directory is
// never an error as long as at least one game's metadata resolves.
func Profiles(gamesDir string) (map[string]gameapi.Profile, error) {
	profiles := map[string]gameapi.Profile{}

	embedded, err := savesyncpspc.Builtin.ReadDir("games")
	if err != nil {
		return nil, err
	}
	sort.Slice(embedded, func(i, j int) bool { return embedded[i].Name() < embedded[j].Name() })
	for _, entry := range embedded {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := savesyncpspc.Builtin.ReadFile("games/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if err := addProfile(profiles, "embedded:"+entry.Name(), raw); err != nil {
			return nil, err
		}
	}

	if gamesDir != "" {
		materializeBuiltinGamesDir(gamesDir)
		paths, err := filepath.Glob(filepath.Join(gamesDir, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if err := addProfile(profiles, path, raw); err != nil {
				return nil, err
			}
		}
	}

	if len(profiles) == 0 {
		return nil, fmt.Errorf("no game metadata available (checked built-ins and %s)", gamesDir)
	}
	return profiles, nil
}

// materializeBuiltinGamesDir seeds gamesDir with a copy of the embedded
// game metadata the first time it's used, so a plain `save-sync ...` run
// from any directory ends up with an editable, visible games/ folder
// there instead of the metadata living only inside the binary. It never
// overwrites a directory or file that already exists, and any failure
// (e.g. a read-only cwd) is silently non-fatal: Profiles already works
// off the embedded defaults with or without this.
func materializeBuiltinGamesDir(gamesDir string) {
	if _, err := os.Stat(gamesDir); err == nil || !os.IsNotExist(err) {
		return
	}
	entries, err := savesyncpspc.Builtin.ReadDir("games")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(gamesDir, entry.Name())
		if _, err := os.Stat(path); err == nil {
			continue
		}
		raw, err := savesyncpspc.Builtin.ReadFile("games/" + entry.Name())
		if err != nil {
			continue
		}
		_ = util.AtomicWrite(path, raw)
	}
}

func addProfile(profiles map[string]gameapi.Profile, source string, raw []byte) error {
	var meta metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("invalid game metadata JSON: %s: %w", source, err)
	}
	key := strings.TrimSpace(meta.Game)
	if key == "" {
		key = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	ids, err := parseIDs(meta)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("no title ids defined in %s", source)
	}
	name := meta.Name
	if name == "" {
		name = key
	}
	profiles[key] = gameapi.Profile{
		Key:          key,
		Name:         name,
		TitleIDs:     ids,
		MetadataPath: source,
		Engine:       meta.Engine,
		EngineConfig: meta.EngineConfig,
	}
	return nil
}

func parseIDs(meta metadata) ([]string, error) {
	if meta.ID != "" {
		return []string{strings.ToUpper(meta.ID)}, nil
	}
	if len(meta.IDs) == 0 {
		return nil, nil
	}
	var stringIDs []string
	if err := json.Unmarshal(meta.IDs, &stringIDs); err == nil {
		return upperNonEmpty(stringIDs), nil
	}
	var objectIDs []metadataID
	if err := json.Unmarshal(meta.IDs, &objectIDs); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(objectIDs))
	for _, item := range objectIDs {
		values = append(values, item.ID)
	}
	return upperNonEmpty(values), nil
}

func upperNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, strings.ToUpper(value))
		}
	}
	return out
}

func SelectProfile(gamesDir, gameKey, titleID string, seenTitleIDs []string) (Selected, error) {
	profiles, err := Profiles(gamesDir)
	if err != nil {
		return Selected{}, err
	}
	if gameKey != "" {
		profile, ok := profiles[gameKey]
		if !ok {
			return Selected{}, fmt.Errorf("unknown game %q", gameKey)
		}
		return resolve(profile)
	}
	if titleID != "" {
		titleID = strings.ToUpper(titleID)
		for _, profile := range profiles {
			if contains(profile.TitleIDs, titleID) {
				return resolve(profile)
			}
		}
		return Selected{}, fmt.Errorf("no game metadata maps title id %s", titleID)
	}
	seen := map[string]bool{}
	for _, id := range seenTitleIDs {
		seen[strings.ToUpper(id)] = true
	}
	var matches []gameapi.Profile
	for _, profile := range profiles {
		for _, id := range profile.TitleIDs {
			if seen[id] {
				matches = append(matches, profile)
				break
			}
		}
	}
	if len(matches) == 0 {
		return Selected{}, fmt.Errorf("could not auto-discover a supported game from Garlic saves")
	}
	if len(matches) > 1 {
		return Selected{}, fmt.Errorf("multiple supported games found; pass --game")
	}
	return resolve(matches[0])
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
