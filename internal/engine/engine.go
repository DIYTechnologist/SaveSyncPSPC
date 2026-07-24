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

// Tier classifies a portability check's failure severity.
type Tier int

const (
	// TierConvertible means every applicable check passed: a known-safe
	// convert.
	TierConvertible Tier = 1
	// TierBlocked means one specific, named check failed. Blocked checks
	// can be bypassed one at a time via an override - discovering a new
	// game's compatible envelope graft requires bypassing exactly this
	// tier once to test it, so this is the primary authoring workflow,
	// not just a safety valve.
	TierBlocked Tier = 2
	// TierWrongFormat means the payload isn't structurally the thing the
	// engine expects at all. Never overridable: no override token exists
	// for these checks, because bypassing one would produce garbage with
	// certainty rather than uncertainty.
	TierWrongFormat Tier = 3
)

// Side identifies which half of a conversion a payload represents.
type Side int

const (
	SidePC Side = iota
	SidePS5
)

// CheckResult is the outcome of one named portability check.
type CheckResult struct {
	// Logical is the save image this check ran against.
	Logical string
	Check   string
	Tier    Tier
	Passed  bool
	// Overridden is true when Passed is false only because a caller
	// supplied override bypassed it.
	Overridden bool
	// Warn is set when a check Passed but callers should still surface
	// Reason as a warning (e.g. an unverified class-equivalence
	// candidate: allowed to proceed, but not yet confirmed safe).
	Warn   bool
	Reason string
}

// Verdict is the result of running every applicable portability check
// against one payload (or one converted pair, for pairwise checks).
type Verdict struct {
	// Portable is true if every check either passed or was overridden.
	Portable bool
	// Tier is the highest-severity tier among failing, non-overridden
	// checks (0 if Portable).
	Tier   Tier
	Checks []CheckResult
}

// Blocking returns the checks that failed and were not overridden - the
// reasons a Verdict isn't Portable.
func (v Verdict) Blocking() []CheckResult {
	var out []CheckResult
	for _, c := range v.Checks {
		if !c.Passed && !c.Overridden {
			out = append(out, c)
		}
	}
	return out
}

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

	// OverrideTokens lists every check name valid for this engine's
	// --allow flag, so callers can reject an unknown token with the
	// valid list rather than have it silently bypass nothing.
	OverrideTokens() []string

	// Images lists the Garlic save images this profile needs, in the
	// order backups/conversions should process them.
	Images(cfg any) []gameapi.SaveImage

	// Compatibility describes the PC<->PS5 relationship for display
	// (e.g. the UI's game list), derived from cfg.
	Compatibility(cfg any) gameapi.Compatibility

	// Inspect runs the portability gate against one payload for one
	// logical image, writing nothing. overrides names tier-2 checks to
	// bypass (tier-3 checks are never overridable, so are unaffected by
	// overrides). Used both by `save-sync inspect` and internally by
	// ConvertFromPS5/ConvertToPS5 before any graft or write.
	Inspect(cfg any, logical string, payload []byte, side Side, overrides map[string]bool) Verdict

	ConvertFromPS5(cfg any, ps5Payloads map[string][]byte, pcDir string, overrides map[string]bool) (gameapi.ConversionResult, error)
	ConvertToPS5(cfg any, pcDir string, ps5Templates map[string][]byte, overrides map[string]bool) (gameapi.ConversionResult, error)
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
