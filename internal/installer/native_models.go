package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type ModelChoice struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

const modelLuna = "gpt-5.6-luna"
const modelAstra = "gpt-6-astra"

type ModelOption struct {
	Model   string
	Efforts []string
}

type ModelRole struct{ ID, Label string }

var ModelRoles = []ModelRole{
	{"principal", "Principal"}, {"default", "Subagente genérico"},
	{"explorer", "Explorador"}, {"fallback_explorer", "Explorador de respaldo"},
	{"critical_explorer", "Exploración crítica"}, {"prewalk_executor", "Implementador"},
	{"fallback_executor", "Implementador de respaldo"}, {"engineering_reviewer", "Reviewer"},
}

func DefaultModelChoices() map[string]ModelChoice {
	out := make(map[string]ModelChoice)
	for _, role := range ModelRoles {
		out[role.ID] = ModelChoice{"gpt-5.3-codex-spark", "medium"}
	}
	out["fallback_explorer"], out["fallback_executor"] = ModelChoice{modelLuna, "medium"}, ModelChoice{modelLuna, "medium"}
	out["principal"] = ModelChoice{modelAstra, "medium"}
	out["engineering_reviewer"] = ModelChoice{"gpt-5.6-sol", "xhigh"}
	return out
}

var nativeOptions = sync.OnceValue(func() []ModelOption {
	// Offline defaults match the preset. Prefer metadata shipped by the local
	// Codex binary; never contact a gateway or copy a provider's catalogue.
	out := []ModelOption{
		{modelAstra, []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{"gpt-5.6-sol", []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{modelLuna, []string{"low", "medium", "high", "xhigh", "max"}},
		{"gpt-5.3-codex-spark", []string{"low", "medium", "high", "xhigh"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "codex", "debug", "models", "--bundled").Output()
	if err != nil {
		return out
	}
	return mergeNativeCatalog(out, b)
})

func mergeNativeCatalog(out []ModelOption, b []byte) []ModelOption {
	var catalog struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
			Levels     []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &catalog) != nil {
		return out
	}
	for _, m := range catalog.Models {
		if m.Visibility != "list" || !strings.HasPrefix(m.Slug, "gpt-") || len(m.Levels) == 0 {
			continue
		}
		option := ModelOption{Model: m.Slug}
		for _, level := range m.Levels {
			option.Efforts = append(option.Efforts, level.Effort)
		}
		found := false
		for i := range out {
			if out[i].Model == m.Slug {
				out[i] = option
				found = true
				break
			}
		}
		if !found {
			out = append(out, option)
		}
	}
	return out
}

func NativeModelOptions() []ModelOption {
	out := make([]ModelOption, len(nativeOptions()))
	for i, option := range nativeOptions() {
		out[i] = ModelOption{option.Model, append([]string(nil), option.Efforts...)}
	}
	return out
}

func (e *Engine) BuildPlanWithModels(ids []string, choices map[string]ModelChoice) (*Plan, error) {
	return e.buildPlanWithOptions(ids, choices, NativeModelOptions())
}

func (e *Engine) buildPlanWithOptions(ids []string, choices map[string]ModelChoice, options []ModelOption) (*Plan, error) {
	copy := DefaultModelChoices()
	for id, choice := range choices {
		if _, ok := copy[id]; !ok {
			return nil, fmt.Errorf("rol desconocido: %s", id)
		}
		valid := false
		for _, option := range options {
			if option.Model == choice.Model && hasString(option.Efforts, choice.Effort) {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("modelo/esfuerzo nativo no disponible para %s: %s/%s", id, choice.Model, choice.Effort)
		}
		copy[id] = choice
	}
	snapshot := *e
	snapshot.modelChoices = copy
	p, err := snapshot.BuildPlan(ids)
	if p != nil {
		p.owner = e
	}
	return p, err
}

func (e *Engine) nativeConfig(p *Plan, get func(string) (*Change, error)) error {
	choices := e.modelChoices
	if choices == nil {
		choices = DefaultModelChoices()
	}
	update := func(path string, root bool, choice ModelChoice) error {
		c, err := get(path)
		if err != nil {
			return err
		}
		if !root && !c.existed && len(c.data) == 0 {
			return nil
		}
		config := map[string]any{}
		if err = toml.Unmarshal(c.data, &config); err != nil {
			return err
		}
		for _, key := range []string{"model_provider", "model_providers", "model_catalog_json"} {
			delete(config, key)
		}
		config["model"], config["model_reasoning_effort"] = choice.Model, choice.Effort
		// Generic subagents inherit the root setting. Their Spark default needs
		// this too; preserve explicit summaries on other named model roles.
		if choice.Model == "gpt-5.3-codex-spark" || (root && choices["default"].Model == "gpt-5.3-codex-spark") {
			config["model_reasoning_summary"] = "none"
		}
		if root {
			config["forced_login_method"] = "chatgpt"
			features, _ := config["features"].(map[string]any)
			if features == nil {
				features = map[string]any{}
			}
			features["multi_agent"], features["multi_agent_v2"] = true, true
			config["features"] = features
			agents, _ := config["agents"].(map[string]any)
			if agents == nil {
				agents = map[string]any{}
			}
			agents["default_subagent_model"], agents["default_subagent_reasoning_effort"] = choices["default"].Model, choices["default"].Effort
			config["agents"] = agents
		}
		c.data, err = toml.Marshal(config)
		return err
	}
	if err := update(filepath.Join(e.CodexHome, "config.toml"), true, choices["principal"]); err != nil {
		return err
	}
	prewalk := false
	for _, module := range p.Modules {
		if module.ID == "prewalk" {
			prewalk = true
		}
	}
	for _, role := range ModelRoles {
		if !prewalk {
			break
		}
		if role.ID == "principal" || role.ID == "default" {
			continue
		}
		if err := update(filepath.Join(e.CodexHome, "agents", role.ID+".toml"), false, choices[role.ID]); err != nil {
			return err
		}
	}
	p.Warnings = append(p.Warnings, "Modelos nativos de la suscripción ChatGPT: se retira la configuración de proveedores y catálogos externos. Los cambios requieren confirmar la instalación.")
	return nil
}
