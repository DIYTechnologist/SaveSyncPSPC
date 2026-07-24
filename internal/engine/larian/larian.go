// Package larian will implement engine.Engine for Larian's LSPK save
// container format, used by Baldur's Gate 3. Conversion is not implemented
// yet: this stub exists so a games/<key>.json profile can already declare
// "engine": "larian" and resolve to a real (if inert) Engine rather than
// an unknown-engine error, ahead of the LSPK reader/writer work.
package larian

import (
	"encoding/json"
	"fmt"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
)

// Config is the engine_config block for a games/<key>.json profile using
// the "larian" engine. Empty for now; the real shape (images, mod-parity
// reference image, build-order constraints) lands with the conversion
// implementation.
type Config struct{}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Name() string { return "larian" }

func (Engine) OverrideTokens() []string { return nil }

func (Engine) ParseConfig(raw json.RawMessage) (any, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid larian engine_config: %w", err)
	}
	return cfg, nil
}

func (Engine) Images(any) []gameapi.SaveImage { return nil }

func (Engine) Compatibility(any) gameapi.Compatibility { return gameapi.Compatibility{} }

var errNotImplemented = fmt.Errorf("larian (Baldur's Gate 3) conversion is not implemented yet")

func (Engine) Inspect(any, string, []byte, engine.Side, map[string]bool) engine.Verdict {
	return engine.Verdict{
		Checks: []engine.CheckResult{{
			Check:  "not-implemented",
			Tier:   engine.TierWrongFormat,
			Passed: false,
			Reason: errNotImplemented.Error(),
		}},
	}
}

func (Engine) ConvertFromPS5(any, map[string][]byte, string, map[string]bool) (gameapi.ConversionResult, error) {
	return gameapi.ConversionResult{}, errNotImplemented
}

func (Engine) ConvertToPS5(any, string, map[string][]byte, map[string]bool) (gameapi.ConversionResult, error) {
	return gameapi.ConversionResult{}, errNotImplemented
}

func (Engine) InstallOutputs(any, map[string][]byte, string, string) error {
	return errNotImplemented
}
