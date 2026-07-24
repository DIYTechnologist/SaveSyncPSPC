package clair

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"savesyncpspc/internal/gameapi"
	"savesyncpspc/internal/gvas"
	"savesyncpspc/internal/util"
)

const (
	PCMain      = "EXPEDITION_0.sav"
	PCContainer = "SavesContainer.sav"
	PayloadName = "ue4savegame.dpx.sav"
)

type Game struct{}

var images = []gameapi.SaveImage{
	{Logical: "gameplay", SaveName: "sdimg_EXPEDITION0", Label: "EXPEDITION_0", PCFile: PCMain},
	{Logical: "container", SaveName: "sdimg_SavesContainer", Label: "SavesContainer", PCFile: PCContainer},
}

func (Game) Key() string  { return "clair" }
func (Game) Name() string { return "Clair Obscur: Expedition 33" }
func (Game) TitleIDs() []string {
	return []string{"PPSA17599"}
}
func (Game) PayloadName() string { return PayloadName }

func (Game) SaveImages() []gameapi.SaveImage {
	out := make([]gameapi.SaveImage, len(images))
	copy(out, images)
	return out
}

func (Game) Compatibility() gameapi.Compatibility {
	return gameapi.Compatibility{
		PC: gameapi.CompatibilitySide{
			Platform:            "Steam",
			GameplayClassSuffix: "BP_SaveGameObject_V8_C",
			Version:             "V8",
		},
		PS5: gameapi.CompatibilitySide{
			Platform:            "PS5",
			GameplayClassSuffix: "BP_SaveGameObject_V7_C",
			Version:             "V7",
		},
		Convertible: true,
		Note:        "Known compatible envelope graft: Steam gameplay V8 <-> PS5 gameplay V7.",
	}
}

func validatePCDir(pcDir string) (string, string, error) {
	main := filepath.Join(pcDir, PCMain)
	container := filepath.Join(pcDir, PCContainer)
	var missing []string
	for _, path := range []string{main, container} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("missing PC save file(s): %s", strings.Join(missing, ", "))
	}
	return main, container, nil
}

func classWarnings(source, target gvas.Info, logical string, direction string) []string {
	if logical != "gameplay" {
		return nil
	}
	compat := Game{}.Compatibility()
	var warnings []string
	switch direction {
	case "ps5-to-pc":
		if !strings.HasSuffix(source.SaveClass, compat.PS5.GameplayClassSuffix) {
			warnings = append(warnings, fmt.Sprintf("Expected PS5 gameplay class ending %s, got %s", compat.PS5.GameplayClassSuffix, source.SaveClass))
		}
		if !strings.HasSuffix(target.SaveClass, compat.PC.GameplayClassSuffix) {
			warnings = append(warnings, fmt.Sprintf("Expected Steam gameplay class ending %s, got %s", compat.PC.GameplayClassSuffix, target.SaveClass))
		}
	case "pc-to-ps5":
		if !strings.HasSuffix(source.SaveClass, compat.PC.GameplayClassSuffix) {
			warnings = append(warnings, fmt.Sprintf("Expected Steam gameplay class ending %s, got %s", compat.PC.GameplayClassSuffix, source.SaveClass))
		}
		if !strings.HasSuffix(target.SaveClass, compat.PS5.GameplayClassSuffix) {
			warnings = append(warnings, fmt.Sprintf("Expected PS5 gameplay class ending %s, got %s", compat.PS5.GameplayClassSuffix, target.SaveClass))
		}
	}
	return warnings
}

func (Game) ConvertFromPS5(ps5Payloads map[string][]byte, pcDir string) (gameapi.ConversionResult, error) {
	mainPath, containerPath, err := validatePCDir(pcDir)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	pcMain, err := os.ReadFile(mainPath)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	pcContainer, err := os.ReadFile(containerPath)
	if err != nil {
		return gameapi.ConversionResult{}, err
	}

	mainOut, mainSource, mainTemplate, mainResult, err := gvas.ConvertWithEnvelope(ps5Payloads["gameplay"], pcMain, "Garlic PS5 EXPEDITION_0", "PC EXPEDITION_0 template")
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	containerOut, containerSource, containerTemplate, containerResult, err := gvas.ConvertWithEnvelope(ps5Payloads["container"], pcContainer, "Garlic PS5 SavesContainer", "PC SavesContainer template")
	if err != nil {
		return gameapi.ConversionResult{}, err
	}
	warnings := classWarnings(mainSource, mainTemplate, "gameplay", "ps5-to-pc")
	return gameapi.ConversionResult{
		Outputs: map[string][]byte{
			PCMain:      mainOut,
			PCContainer: containerOut,
		},
		Warnings: warnings,
		Manifest: map[string]any{
			"pc_dir":        pcDir,
			"compatibility": Game{}.Compatibility(),
			"warnings":      warnings,
			"main": map[string]any{
				"source":   mainSource,
				"template": mainTemplate,
				"result":   mainResult,
			},
			"container": map[string]any{
				"source":   containerSource,
				"template": containerTemplate,
				"result":   containerResult,
			},
		},
	}, nil
}

func (Game) ConvertToPS5(pcDir string, ps5Templates map[string][]byte) (gameapi.ConversionResult, error) {
	if _, _, err := validatePCDir(pcDir); err != nil {
		return gameapi.ConversionResult{}, err
	}
	outputs := map[string][]byte{}
	manifest := map[string]any{
		"pc_dir":        pcDir,
		"compatibility": Game{}.Compatibility(),
		"warnings":      []string{},
	}
	var warnings []string
	for _, image := range images {
		pcPath := filepath.Join(pcDir, image.PCFile)
		pcData, err := os.ReadFile(pcPath)
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		output, source, target, result, err := gvas.ConvertWithEnvelope(pcData, ps5Templates[image.Logical], "PC "+image.Label, "Garlic PS5 "+image.Label+" template")
		if err != nil {
			return gameapi.ConversionResult{}, err
		}
		outputs[image.SaveName] = output
		warnings = append(warnings, classWarnings(source, target, image.Logical, "pc-to-ps5")...)
		manifest[image.Logical] = map[string]any{
			"source":          source,
			"target_template": target,
			"result":          result,
			"save_name":       image.SaveName,
			"payload_name":    PayloadName,
		}
	}
	manifest["warnings"] = warnings
	return gameapi.ConversionResult{Outputs: outputs, Manifest: manifest, Warnings: warnings}, nil
}

func (Game) InstallOutputs(outputs map[string][]byte, pcDir string, backupDir string) error {
	if backupDir == "" {
		backupDir = filepath.Join(pcDir, "_pre_garlic_sync_"+time.Now().Format("20060102_150405"))
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return err
		}
	} else if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{PCMain, PCContainer} {
		source := filepath.Join(pcDir, name)
		backup := filepath.Join(backupDir, name)
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			if err := util.CopyFile(source, backup); err != nil {
				return err
			}
		}
		if err := util.AtomicWrite(source, outputs[name]); err != nil {
			return err
		}
	}
	return nil
}
