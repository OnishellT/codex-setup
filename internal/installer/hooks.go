package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const codexHomeShellPlaceholder = "{{CODEX_HOME_SHELL}}"

// mergeHooks installs only hook groups owned by this module. Ownership is
// intentionally declared in the user-visible statusMessage rather than an
// invented JSON field, so Codex continues to validate the resulting file.
func (e *Engine) mergeHooks(p *Plan, moduleID, source string, get func(string) (*Change, error)) error {
	b, err := fs.ReadFile(e.assets, source)
	if err != nil {
		return err
	}
	var addition map[string]any
	if err = json.Unmarshal(b, &addition); err != nil {
		return fmt.Errorf("plantilla hooks JSON inválida %s: %w", source, err)
	}
	additionalHooks, err := hooksTable(addition, source)
	if err != nil {
		return err
	}
	if err = expandHookCommands(additionalHooks, shellQuote(e.CodexHome)); err != nil {
		return fmt.Errorf("plantilla hooks inválida %s: %w", source, err)
	}
	marker := "[codex-setup:" + moduleID + "]"
	if err = validateManagedHookGroups(additionalHooks, marker); err != nil {
		return fmt.Errorf("plantilla hooks inválida %s: %w", source, err)
	}

	filename, err := e.target("codex", "hooks.json")
	if err != nil {
		return err
	}
	c, err := get(filename)
	if err != nil {
		return err
	}
	current := map[string]any{}
	if len(strings.TrimSpace(string(c.data))) != 0 {
		if err = json.Unmarshal(c.data, &current); err != nil {
			return fmt.Errorf("hooks JSON existente inválido %s: %w", filename, err)
		}
		if current == nil {
			return fmt.Errorf("hooks JSON existente inválido %s: debe ser un objeto", filename)
		}
	}
	currentHooks, err := hooksTableOrCreate(current, filename)
	if err != nil {
		return err
	}
	removeManagedHookGroups(currentHooks, marker)
	for event, rawGroups := range additionalHooks {
		groups, ok := rawGroups.([]any)
		if !ok { // validateManagedHookGroups already rejects this; retain defense here.
			return fmt.Errorf("evento %q debe contener una lista de grupos", event)
		}
		existing := []any(nil)
		if rawExisting, exists := currentHooks[event]; exists {
			var valid bool
			existing, valid = rawExisting.([]any)
			if !valid {
				return fmt.Errorf("hooks JSON existente inválido %s: evento %q debe ser una lista", filename, event)
			}
		}
		currentHooks[event] = appendHookGroups(existing, groups)
	}
	if c.existed && reflect.DeepEqual(current, decodeJSONObject(c.data)) {
		return nil
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	c.data = append(encoded, '\n')
	p.Warnings = append(p.Warnings, "Hooks de "+moduleID+" añadidos a "+filename+". Revísalos y confíalos manualmente con /hooks; el instalador no evita la confirmación de confianza.")
	p.Warnings = append(p.Warnings, hookPrerequisiteWarnings(moduleID)...)
	return nil
}

// decodeJSONObject is used only after successful parsing into current. It keeps
// the no-op path byte-preserving when an existing hooks file already represents
// the same configuration.
func decodeJSONObject(data []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

func hooksTable(value map[string]any, label string) (map[string]any, error) {
	raw, ok := value["hooks"]
	if !ok {
		return nil, errors.New("falta la tabla hooks")
	}
	hooks, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("hooks debe ser un objeto")
	}
	return hooks, nil
}

func hooksTableOrCreate(value map[string]any, label string) (map[string]any, error) {
	if raw, ok := value["hooks"]; ok {
		hooks, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hooks JSON existente inválido %s: hooks debe ser un objeto", label)
		}
		return hooks, nil
	}
	hooks := map[string]any{}
	value["hooks"] = hooks
	return hooks, nil
}

func validateManagedHookGroups(events map[string]any, marker string) error {
	if len(events) == 0 {
		return errors.New("hooks no puede estar vacío")
	}
	for event, rawGroups := range events {
		groups, ok := rawGroups.([]any)
		if !ok || len(groups) == 0 {
			return fmt.Errorf("evento %q debe contener una lista no vacía de grupos", event)
		}
		for i, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				return fmt.Errorf("evento %q grupo %d debe ser un objeto", event, i)
			}
			handlers, ok := group["hooks"].([]any)
			if !ok || len(handlers) == 0 {
				return fmt.Errorf("evento %q grupo %d debe incluir hooks", event, i)
			}
			owned := false
			for _, rawHandler := range handlers {
				handler, ok := rawHandler.(map[string]any)
				if !ok {
					return fmt.Errorf("evento %q grupo %d contiene un handler inválido", event, i)
				}
				if message, _ := handler["statusMessage"].(string); strings.HasPrefix(message, marker) {
					owned = true
				}
			}
			if !owned {
				return fmt.Errorf("evento %q grupo %d no contiene statusMessage con %q", event, i, marker)
			}
		}
	}
	return nil
}

func expandHookCommands(value any, quotedCodexHome string) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "command" {
				command, ok := child.(string)
				if !ok {
					return errors.New("command debe ser texto")
				}
				v[key] = strings.ReplaceAll(command, codexHomeShellPlaceholder, quotedCodexHome)
				continue
			}
			if err := expandHookCommands(child, quotedCodexHome); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := expandHookCommands(child, quotedCodexHome); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeManagedHookGroups(events map[string]any, marker string) {
	for event, rawGroups := range events {
		groups, ok := rawGroups.([]any)
		if !ok {
			continue // Foreign malformed event is preserved for Codex to report.
		}
		kept := make([]any, 0, len(groups))
		for _, rawGroup := range groups {
			group, keep := removeManagedHookHandlers(rawGroup, marker)
			if keep {
				kept = append(kept, group)
			}
		}
		if len(kept) == 0 {
			delete(events, event)
		} else {
			events[event] = kept
		}
	}
}

// removeManagedHookHandlers deliberately keeps foreign handlers that share a
// matcher group with an installed one. The module owns handlers, not groups.
func removeManagedHookHandlers(rawGroup any, marker string) (any, bool) {
	group, ok := rawGroup.(map[string]any)
	if !ok {
		return rawGroup, true
	}
	handlers, ok := group["hooks"].([]any)
	if !ok {
		return rawGroup, true
	}
	kept := make([]any, 0, len(handlers))
	for _, rawHandler := range handlers {
		handler, ok := rawHandler.(map[string]any)
		if !ok {
			kept = append(kept, rawHandler)
			continue
		}
		if message, _ := handler["statusMessage"].(string); strings.HasPrefix(message, marker) {
			continue
		}
		kept = append(kept, rawHandler)
	}
	if len(kept) == 0 {
		return nil, false
	}
	group["hooks"] = kept
	return group, true
}

func appendHookGroups(existing, addition []any) []any {
	result := make([]any, 0, len(existing)+len(addition))
	result = append(result, existing...)
	result = append(result, addition...)
	return result
}

func hookPrerequisiteWarnings(moduleID string) []string {
	warnings := []string{}
	if _, err := exec.LookPath("python3"); err != nil {
		warnings = append(warnings, moduleID+": falta python3 en PATH; el hook quedará instalado pero no podrá ejecutarse.")
	}
	switch moduleID {
	case "rtk":
		path, err := exec.LookPath("rtk")
		if err != nil {
			return append(warnings, "rtk: falta RTK >= 0.23 en PATH; el adaptador dejará pasar los comandos sin cambios.")
		}
		out, err := run(2*time.Second, path, "--version")
		if err != nil || !atLeastVersion(out, 0, 23) {
			warnings = append(warnings, "rtk: se requiere RTK >= 0.23 (no se pudo verificar la versión instalada).")
		}
	case "ponytail":
		path, err := exec.LookPath("node")
		if err != nil {
			return append(warnings, "ponytail: falta Node.js >= 18 en PATH; los hooks quedarán instalados pero no podrán ejecutarse.")
		}
		out, err := run(2*time.Second, path, "--version")
		if err != nil || !atLeastVersion(out, 18, 0) {
			warnings = append(warnings, "ponytail: se requiere Node.js >= 18 (no se pudo verificar la versión instalada).")
		}
	}
	return warnings
}

var versionPattern = regexp.MustCompile(`(?:^|\D)(\d+)\.(\d+)`)

func atLeastVersion(output string, wantMajor, wantMinor int) bool {
	parts := versionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if parts == nil {
		return false
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	return major > wantMajor || major == wantMajor && minor >= wantMinor
}

// enableHooksFeature is part of the same plan as hooks.json, so Apply rolls
// both back if either write fails. It intentionally leaves every other feature
// (notably plugins) untouched.
func (e *Engine) enableHooksFeature(get func(string) (*Change, error)) error {
	filename, err := e.target("codex", "config.toml")
	if err != nil {
		return err
	}
	c, err := get(filename)
	if err != nil {
		return err
	}
	settings := map[string]any{}
	if err = toml.Unmarshal(c.data, &settings); err != nil {
		return fmt.Errorf("TOML existente inválido %s: %w", filename, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	features, ok := settings["features"].(map[string]any)
	if !ok && settings["features"] != nil {
		return errors.New("features debe ser una tabla TOML")
	}
	if features == nil {
		features = map[string]any{}
		settings["features"] = features
	}
	if enabled, ok := features["hooks"].(bool); ok && enabled {
		return nil
	}
	features["hooks"] = true
	c.data, err = toml.Marshal(settings)
	return err
}
