package gameapi

import "encoding/json"

type CompatibilitySide struct {
	Platform            string `json:"platform"`
	GameplayClassSuffix string `json:"gameplay_class_suffix"`
	Version             string `json:"version"`
}

type Compatibility struct {
	PC          CompatibilitySide `json:"pc"`
	PS5         CompatibilitySide `json:"ps5"`
	Convertible bool              `json:"convertible"`
	Note        string            `json:"note"`
}

type SaveImage struct {
	Logical  string `json:"logical"`
	SaveName string `json:"save_name"`
	Label    string `json:"label"`
	PCFile   string `json:"pc_file"`
	// Payload is the filename inside the Garlic save image (e.g.
	// "ue4savegame.dpx.sav"). It's a property of the image, not the game,
	// since different logical images of the same game - or a different
	// engine entirely - may use different payload filenames.
	Payload string `json:"payload"`

	// The Dynamic* fields exist for engines (e.g. Baldur's Gate 3) whose
	// save-image identity isn't a fixed convention the way Clair's is:
	// there's no single "sdimg_EXPEDITION0"-equivalent constant, because
	// each save slot and each save file is named per-instance. When a
	// Dynamic* flag is set, the corresponding field above is a
	// placeholder (typically "") and the real value must be resolved at
	// runtime - see engine.Engine's ResolvePayload/ResolvePCFile and
	// bridge.go's resolution pass, which runs before any backup or
	// conversion step.

	// DynamicSaveName means SaveName must be supplied by the caller
	// (--ps5-save-name) rather than read from this struct - there's no
	// way to infer which PS5 save slot the user means.
	DynamicSaveName bool `json:"dynamic_save_name"`
	// DynamicPayload means Payload must be discovered by listing the
	// mounted Garlic save image's actual files (engine.ResolvePayload)
	// rather than assumed from config.
	DynamicPayload bool `json:"dynamic_payload"`
	// DynamicPCFile means PCFile must be discovered by inspecting the PC
	// save directory's actual contents (engine.ResolvePCFile) rather
	// than assumed from config.
	DynamicPCFile bool `json:"dynamic_pc_file"`
}

type ConversionResult struct {
	Outputs  map[string][]byte
	Manifest map[string]any
	Warnings []string
	// OverriddenChecks lists (deduplicated) the portability-gate check
	// names that actually fired an override during this conversion. A
	// caller-supplied --allow token not appearing here bypassed nothing -
	// worth a loud warning, since a no-op override is easy to mistake for
	// a working one.
	OverriddenChecks []string
}

type Profile struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	TitleIDs     []string        `json:"ids"`
	MetadataPath string          `json:"metadata_path"`
	Engine       string          `json:"engine"`
	EngineConfig json.RawMessage `json:"engine_config"`

	// PCSaveDirs maps a Go GOOS value ("windows", "linux", "darwin") to a
	// template path for where this game's PC saves normally live, for
	// suggesting a starting point to browse from (see internal/pcpath).
	// Supported placeholders: %LOCALAPPDATA%, %APPDATA%, %USERPROFILE%
	// (expanded from the current process's environment - meaningful only
	// when actually running on that OS) and a leading ~ for the home
	// directory. Optional; a game with no entry for the current OS simply
	// has no suggestion, the user can still type a path manually.
	PCSaveDirs map[string]string `json:"pc_save_dirs,omitempty"`
	// SteamAppID, if set, lets internal/pcpath also suggest Steam Play
	// (Proton) compatdata locations on Linux by reusing the "windows"
	// entry's path suffix under each guessed Steam library's
	// steamapps/compatdata/<id>/pfx/drive_c/users/steamuser/AppData/Local.
	SteamAppID string `json:"steam_app_id,omitempty"`
}
