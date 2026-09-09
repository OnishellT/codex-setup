package installer

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func readableInstalledFile(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("no es un archivo normal: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Read(make([]byte, 1))
	if err == io.EOF {
		return nil
	}
	return err
}

func (e *Engine) checkInstalledOperation(op Operation) error {
	if op.Kind == "tree" {
		return fs.WalkDir(e.assets, op.Source, func(source string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(op.Source, source)
			if err != nil {
				return err
			}
			target, err := e.target(op.Root, filepath.Join(op.Target, rel))
			if err != nil {
				return err
			}
			return readableInstalledFile(target)
		})
	}
	switch op.Kind {
	case "copy", "copy-if-missing", "template-copy", "agent-instructions", "qlty-install", "prewalk-settings", "merge", "append", "developer-instructions", "native-config", "prewalk-config", "hooks-state":
		target, err := e.target(op.Root, op.Target)
		if err != nil {
			return err
		}
		return readableInstalledFile(target)
	}
	return nil
}

func (e *Engine) checkInstalledFiles(modules []Module) []string {
	var pending []string
	for _, m := range modules {
		for _, op := range m.Operations {
			if err := e.checkInstalledOperation(op); err != nil {
				pending = append(pending, fmt.Sprintf("%s: %v", m.ID, err))
			}
		}
	}
	return pending
}

func installedModelConfig(path string) (map[string]any, error) {
	if err := readableInstalledFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	for _, key := range []string{"model_provider", "model_providers", "model_catalog_json"} {
		if _, ok := c[key]; ok {
			return nil, fmt.Errorf("configuración de proveedor externo sin verificar")
		}
	}
	return c, nil
}

func (e *Engine) checkInstalledModels(status *AccountStatus, modules []Module) []string {
	if status == nil || len(status.Models) == 0 {
		return []string{"cuenta: catálogo no verificado"}
	}
	// Models are only configured by native-config (base/agents), not by standalone hooks.
	native, prewalk := false, false
	for _, m := range modules {
		prewalk = prewalk || m.ID == "prewalk"
		for _, op := range m.Operations {
			native = native || op.Kind == "native-config"
		}
	}
	if !native && !prewalk {
		return nil
	}
	config, err := installedModelConfig(filepath.Join(e.CodexHome, "config.toml"))
	if err != nil {
		return []string{fmt.Sprintf("modelos: %v", err)}
	}
	var pending []string
	check := func(c map[string]any, modelKey, effortKey, label string) {
		model, _ := c[modelKey].(string)
		effort, _ := c[effortKey].(string)
		if !modelChoiceAvailable(status, ModelChoice{model, effort}) {
			pending = append(pending, label+": modelo/esfuerzo no disponible en la cuenta")
		}
	}
	check(config, "model", "model_reasoning_effort", "principal")
	agents, _ := config["agents"].(map[string]any)
	check(agents, "default_subagent_model", "default_subagent_reasoning_effort", "default")
	if !prewalk {
		return pending
	}
	for _, role := range ModelRoles {
		if role.ID == "principal" || role.ID == "default" {
			continue
		}
		c, err := installedModelConfig(filepath.Join(e.CodexHome, "agents", role.ID+".toml"))
		if err != nil {
			pending = append(pending, fmt.Sprintf("%s: %v", role.ID, err))
			continue
		}
		check(c, "model", "model_reasoning_effort", role.ID)
	}
	return pending
}

func (e *Engine) readinessFilesAndModels(modules []Module, status *AccountStatus) []string {
	out := e.checkInstalledFiles(modules)
	out = append(out, e.checkInstalledModels(status, modules)...)
	for i := range out {
		if !strings.Contains(out[i], "vuelve") {
			out[i] += "; corrige y vuelve a comprobar"
		}
	}
	return out
}
