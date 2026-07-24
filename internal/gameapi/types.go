package gameapi

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
}

type ConversionResult struct {
	Outputs  map[string][]byte
	Manifest map[string]any
	Warnings []string
}

type Profile struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	TitleIDs     []string `json:"ids"`
	MetadataPath string   `json:"metadata_path"`
}

type Game interface {
	Key() string
	Name() string
	TitleIDs() []string
	PayloadName() string
	SaveImages() []SaveImage
	Compatibility() Compatibility
	ConvertFromPS5(ps5Payloads map[string][]byte, pcDir string) (ConversionResult, error)
	ConvertToPS5(pcDir string, ps5Templates map[string][]byte) (ConversionResult, error)
	InstallOutputs(outputs map[string][]byte, pcDir string, backupDir string) error
}

