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
}

type ConversionResult struct {
	Outputs  map[string][]byte
	Manifest map[string]any
	Warnings []string
}

type Profile struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	TitleIDs     []string        `json:"ids"`
	MetadataPath string          `json:"metadata_path"`
	Engine       string          `json:"engine"`
	EngineConfig json.RawMessage `json:"engine_config"`
}
