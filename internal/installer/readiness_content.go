package installer

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func (e *Engine) checkInstalledPayload(source, target string, expand, executable bool) error {
	if err := readableInstalledFile(target); err != nil {
		return err
	}
	data, err := fs.ReadFile(e.assets, source)
	if err != nil {
		return err
	}
	if expand {
		data = expandCodexHomeShell(data, e.CodexHome)
	}
	digest, err := fileSHA256(target, int64(len(data)))
	if err != nil || digest != sha256Hex(data) {
		return fmt.Errorf("archivo gestionado modificado: %s", target)
	}
	if executable && (strings.HasSuffix(source, ".sh") || bytes.HasPrefix(data, []byte("#!"))) {
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("archivo no ejecutable: %s", target)
		}
	}
	return nil
}

func checkInstalledQlty(target string) error {
	if err := readableInstalledFile(target); err != nil {
		return err
	}
	digest, err := fileSHA256(target, qltyMaxBinarySize)
	if err != nil || digest != qltyReleases[qltyPlatform()].binarySHA256 {
		return fmt.Errorf("Qlty ausente o modificado")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("Qlty no ejecutable")
	}
	return nil
}

func (e *Engine) checkInstalledConfig(op Operation, target string) error {
	config, err := installedModelConfig(target)
	if err != nil {
		return err
	}
	if op.Kind == "native-config" {
		features, _ := config["features"].(map[string]any)
		if config["forced_login_method"] != "chatgpt" || features["multi_agent"] != true || features["multi_agent_v2"] != true {
			return fmt.Errorf("configuración nativa ChatGPT/multiagente deshabilitada")
		}
	}
	if op.Source == "" {
		return nil
	}
	data, err := fs.ReadFile(e.assets, op.Source)
	if err != nil {
		return err
	}
	var expected map[string]any
	if err := toml.Unmarshal(data, &expected); err != nil {
		return err
	}
	expandConfigPaths(expected, e.CodexHome)
	// Available account choices supersede packaged presets.
	delete(expected, "model")
	delete(expected, "model_reasoning_effort")
	if agents, ok := expected["agents"].(map[string]any); ok {
		delete(agents, "default_subagent_model")
		delete(agents, "default_subagent_reasoning_effort")
	}
	if !containsManagedConfig(config, expected) {
		return fmt.Errorf("configuración gestionada ausente o modificada: %s", target)
	}
	return nil
}

func containsManagedConfig(actual, expected map[string]any) bool {
	for key, value := range expected {
		if wanted, ok := value.(map[string]any); ok {
			current, ok := actual[key].(map[string]any)
			if !ok || !containsManagedConfig(current, wanted) {
				return false
			}
		} else if !reflect.DeepEqual(actual[key], value) {
			return false
		}
	}
	return true
}

func (e *Engine) checkInstalledSkills(op Operation) error {
	config, err := installedModelConfig(filepath.Join(e.CodexHome, "config.toml"))
	if err != nil {
		return err
	}
	skills, _ := config["skills"].(map[string]any)
	entries, _ := skills["config"].([]any)
	return fs.WalkDir(e.assets, op.Source, func(source string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
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
		if !installedSkillMatches(entries, target, op.Enabled) {
			return fmt.Errorf("skill ausente, duplicada o deshabilitada: %s", target)
		}
		return nil
	})
}

func installedSkillMatches(entries []any, target string, wanted bool) bool {
	count, matches := 0, false
	for _, raw := range entries {
		item, _ := raw.(map[string]any)
		if item["path"] == target || item["path"] == filepath.Dir(target) {
			count++
			matches = item["enabled"] == wanted
		}
	}
	return count == 1 && matches
}

func (e *Engine) checkInstalledPanel() error {
	get := func(target string) (*Change, error) {
		if err := readableInstalledFile(target); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return nil, err
		}
		return &Change{data: data, mode: info.Mode()}, nil
	}
	put := func(target string, expected []byte, mode fs.FileMode) error {
		current, err := get(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(current.data, expected) || (mode&0111 != 0 && current.mode&0111 == 0) {
			return fmt.Errorf("integración del panel ausente o modificada: %s", target)
		}
		return nil
	}
	// Compile only into the helper's disposable directory and compare generated
	// launchers, terminfo and shell blocks without writing any installed file.
	return e.preparePanel(&Plan{}, put, get)
}
