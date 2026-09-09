package installer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type Readiness struct{ Pending []string }

func (r *Readiness) Ready() bool { return r != nil && len(r.Pending) == 0 }

// CheckReadiness never changes native hook trust or configuration.
func (e *Engine) CheckReadiness(ids []string, status *AccountStatus) (*Readiness, error) {
	modules, err := e.Resolve(ids)
	if err != nil {
		return nil, err
	}
	r := &Readiness{}
	r.Pending = append(r.Pending, e.readinessFilesAndModels(modules, status)...)
	for _, m := range modules {
		for _, op := range m.Operations {
			if op.Kind != "hooks-state" {
				continue
			}
			if err := e.checkModuleHooks(op.Source, status); err != nil {
				r.Pending = append(r.Pending, fmt.Sprintf("%s: %v; revisa /hooks y vuelve a comprobar", m.ID, err))
			}
		}
	}
	return r, nil
}

type installedHandler struct {
	event, message string
	group, body    map[string]any
}

func hookHandlers(data []byte) ([]installedHandler, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	events, err := hooksTable(doc, "hooks.json")
	if err != nil {
		return nil, err
	}
	out := []installedHandler{}
	for event, raw := range events {
		if event == "" {
			return nil, fmt.Errorf("evento vacío")
		}
		groups, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("grupos inválidos: %s", event)
		}
		for _, rawGroup := range groups {
			handlers, err := groupHandlers(event, rawGroup)
			if err != nil {
				return nil, err
			}
			out = append(out, handlers...)
		}
	}
	return out, nil
}

func groupHandlers(event string, raw any) ([]installedHandler, error) {
	group, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("grupo inválido: %s", event)
	}
	handlers, ok := group["hooks"].([]any)
	if !ok {
		return nil, fmt.Errorf("handlers inválidos: %s", event)
	}
	delete(group, "hooks")
	out := []installedHandler{}
	for _, rawHandler := range handlers {
		body, ok := rawHandler.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("handler inválido: %s", event)
		}
		message, _ := body["statusMessage"].(string)
		out = append(out, installedHandler{event, message, group, body})
	}
	return out, nil
}

func (e *Engine) expectedHandlers(source string) ([]installedHandler, error) {
	data, err := fs.ReadFile(e.assets, source)
	if err != nil {
		return nil, err
	}
	handlers, err := hookHandlers(data)
	if err != nil {
		return nil, err
	}
	for _, h := range handlers {
		if err := expandHookCommands(h.body, shellQuote(e.CodexHome)); err != nil {
			return nil, err
		}
	}
	if len(handlers) == 0 {
		return nil, fmt.Errorf("plantilla sin handlers")
	}
	return handlers, nil
}

func (e *Engine) checkModuleHooks(source string, status *AccountStatus) error {
	if status == nil || !status.HooksFeatureKnown || !status.HooksEnabled {
		return fmt.Errorf("habilitación de hooks sin verificar")
	}
	if len(status.HookWarnings) != 0 {
		return fmt.Errorf("Codex reportó avisos o errores de hooks")
	}
	expected, err := e.expectedHandlers(source)
	if err != nil {
		return err
	}
	path := filepath.Join(e.CodexHome, "hooks.json")
	if err := checkPath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("falta hooks.json legible")
	}
	installed, err := hookHandlers(data)
	if err != nil {
		return err
	}
	for _, want := range expected {
		if !exactInstalledHandler(want, installed) || !trustedRuntimeHandler(want, path, status.Hooks) {
			return fmt.Errorf("handler ausente, modificado, duplicado, deshabilitado o sin confianza: %s", want.message)
		}
	}
	return nil
}

func exactInstalledHandler(want installedHandler, installed []installedHandler) bool {
	count, exact := 0, false
	for _, got := range installed {
		if got.message == want.message {
			count++
			exact = got.event == want.event && reflect.DeepEqual(got.group, want.group) && reflect.DeepEqual(got.body, want.body)
		}
	}
	return want.message != "" && count == 1 && exact
}

func trustedRuntimeHandler(want installedHandler, path string, hooks []HookStatus) bool {
	count, trusted := 0, false
	event := strings.ToLower(want.event[:1]) + want.event[1:]
	for _, got := range hooks {
		if got.StatusMessage == want.message && got.SourcePath == path {
			count++
			trusted = got.EventName == event && got.Enabled && (got.TrustStatus == "trusted" || got.TrustStatus == "managed")
		}
	}
	return count == 1 && trusted
}
