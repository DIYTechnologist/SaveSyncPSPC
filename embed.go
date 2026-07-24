// Package savesyncpspc embeds the built-in game metadata so the CLI and UI
// binaries work standalone, without needing a games/ directory to be
// present next to wherever the binary happens to be run from. On-disk
// metadata passed via --games-dir still overrides/extends these defaults.
package savesyncpspc

import "embed"

//go:embed games/*.json
var Builtin embed.FS
