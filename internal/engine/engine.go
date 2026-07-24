// Package engine defines the conversion-strategy abstraction that a game
// profile (games/<key>.json) plugs into by name. A "game plugin" used to be
// a Go type hardcoding one title's save-image names, payload filename, and
// class strings; that per-game logic now lives in one Engine implementation
// per save-game engine family (e.g. Unreal's GVAS format), driven by a
// declarative engine_config block in the profile's JSON.
package engine

import (
	"encoding/json"

	"savesyncpspc/internal/gameapi"
)

// Engine implements save conversion for one save-game engine family.
// Config is engine-specific: each Engine parses its own profile's
// engine_config via ParseConfig and type-asserts the result back in its
// other methods. Callers never need to know the concrete Config type.
type Engine interface {
	// Name is the "engine" value a game profile uses to select this
	// Engine, e.g. "unreal".
	Name() string

	// ParseConfig validates and unmarshals a profile's engine_config block.
	ParseConfig(raw json.RawMessage) (any, error)

	// Images lists the Garlic save images this profile needs, in the
	// order backups/conversions should process them.
	Images(cfg any) []gameapi.SaveImage

	// Compatibility describes the PC<->PS5 relationship for display
	// (e.g. the UI's game list), derived from cfg.
	Compatibility(cfg any) gameapi.Compatibility

	ConvertFromPS5(cfg any, ps5Payloads map[string][]byte, pcDir string) (gameapi.ConversionResult, error)
	ConvertToPS5(cfg any, pcDir string, ps5Templates map[string][]byte) (gameapi.ConversionResult, error)
	InstallOutputs(cfg any, outputs map[string][]byte, pcDir string, backupDir string) error
}

var registry = map[string]Engine{}

// Register makes an Engine available for game profiles to select by name.
// Intended to be called once, from internal/games' registry setup.
func Register(e Engine) {
	registry[e.Name()] = e
}

// Get looks up a previously Register'd Engine by name.
func Get(name string) (Engine, bool) {
	e, ok := registry[name]
	return e, ok
}
