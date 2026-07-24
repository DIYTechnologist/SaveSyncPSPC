package games

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/games/clair"
)

func DefaultGamesDir() string {
	return "games"
}

var registry = map[string]gameapi.Game{
	"clair": clair.Game{},
}

type metadata struct {
	Game string          `json:"game"`
	Name string          `json:"name"`
	ID   string          `json:"id"`
	IDs  json.RawMessage `json:"ids"`
}

type metadataID struct {
	ID string `json:"id"`
}

func Registered(key string) (gameapi.Game, bool) {
	game, ok := registry[key]
	return game, ok
}

func Profiles(gamesDir string) (map[string]gameapi.Profile, error) {
	entries, err := filepath.Glob(filepath.Join(gamesDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("games directory contains no metadata: %s", gamesDir)
	}
	sort.Strings(entries)
	profiles := map[string]gameapi.Profile{}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var meta metadata
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("invalid game metadata JSON: %s: %w", path, err)
		}
		key := strings.TrimSpace(meta.Game)
		if key == "" {
			key = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		ids, err := parseIDs(meta)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no title ids defined in %s", path)
		}
		name := meta.Name
		if name == "" {
			name = key
		}
		profiles[key] = gameapi.Profile{Key: key, Name: name, TitleIDs: ids, MetadataPath: path}
	}
	return profiles, nil
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

func SelectProfile(gamesDir, gameKey, titleID string, seenTitleIDs []string) (gameapi.Profile, gameapi.Game, error) {
	profiles, err := Profiles(gamesDir)
	if err != nil {
		return gameapi.Profile{}, nil, err
	}
	if gameKey != "" {
		profile, ok := profiles[gameKey]
		if !ok {
			return gameapi.Profile{}, nil, fmt.Errorf("unknown game %q", gameKey)
		}
		game, ok := Registered(profile.Key)
		if !ok {
			return gameapi.Profile{}, nil, fmt.Errorf("game %q is mapped but not registered in Go", profile.Key)
		}
		return profile, game, nil
	}
	if titleID != "" {
		titleID = strings.ToUpper(titleID)
		for _, profile := range profiles {
			if contains(profile.TitleIDs, titleID) {
				game, ok := Registered(profile.Key)
				if !ok {
					return gameapi.Profile{}, nil, fmt.Errorf("game %q is mapped but not registered in Go", profile.Key)
				}
				return profile, game, nil
			}
		}
		return gameapi.Profile{}, nil, fmt.Errorf("no game metadata maps title id %s", titleID)
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
		return gameapi.Profile{}, nil, fmt.Errorf("could not auto-discover a supported game from Garlic saves")
	}
	if len(matches) > 1 {
		return gameapi.Profile{}, nil, fmt.Errorf("multiple supported games found; pass --game")
	}
	game, ok := Registered(matches[0].Key)
	if !ok {
		return gameapi.Profile{}, nil, fmt.Errorf("game %q is mapped but not registered in Go", matches[0].Key)
	}
	return matches[0], game, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
