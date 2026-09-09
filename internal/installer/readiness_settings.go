package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func (e *Engine) checkInstalledInstructions(moduleID string, op Operation, target string) error {
	if err := readableInstalledFile(target); err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	addition, err := fs.ReadFile(e.assets, op.Source)
	if err != nil {
		return err
	}
	if op.Kind == "developer-instructions" {
		config, err := installedModelConfig(target)
		if err != nil {
			return err
		}
		instruction, _ := config["developer_instructions"].(string)
		current = []byte(instruction)
		addition = expandCodexHomeShell(addition, e.CodexHome)
	}
	expected, err := managedBlock(current, addition, moduleID)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("bloque de instrucciones gestionado ausente o modificado: %s", target)
	}
	return nil
}

func checkInstalledRole(target string) error {
	config, err := installedModelConfig(target)
	if err != nil {
		return err
	}
	instructions, _ := config["developer_instructions"].(string)
	if strings.TrimSpace(instructions) == "" {
		return fmt.Errorf("agente sin instrucciones: %s", target)
	}
	return nil
}

func checkInstalledPrewalkSettings(target string) error {
	if err := readableInstalledFile(target); err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	_, worktreesOK := settings["worktrees"].(bool)
	workers, workersOK := settings["max_workers"].(float64)
	if !worktreesOK || !workersOK || workers < 1 || workers > 4 || workers != float64(int(workers)) {
		return fmt.Errorf("Prewalk: worktrees debe ser booleano y max_workers un entero entre 1 y 4")
	}
	if quality, present := settings["quality"]; present {
		if _, ok := quality.(bool); !ok {
			return fmt.Errorf("Prewalk: quality debe ser booleano")
		}
	}
	return nil
}

func (e *Engine) checkInstalledPrewalkConfig(op Operation, target string) error {
	current, err := installedModelConfig(target)
	if err != nil {
		return err
	}
	source, err := fs.ReadFile(e.assets, op.Source)
	if err != nil {
		return err
	}
	data, err := toml.Marshal(current)
	if err != nil {
		return err
	}
	change := &Change{data: data, existed: true}
	get := func(string) (*Change, error) { return change, nil }
	// Reuse the installer's merge in memory; it preserves explicit permissions
	// and extra writable roots. No post-action or file write is applied here.
	if err := e.prewalkConfig(&Plan{}, target, source, get); err != nil {
		return err
	}
	var expected map[string]any
	if err := toml.Unmarshal(change.data, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return fmt.Errorf("configuración nativa de Prewalk incompleta: %s", target)
	}
	return nil
}
