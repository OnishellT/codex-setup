package installer_test

import (
	"path/filepath"
	"testing"

	"codex-setup/internal/installer"
)

func TestNativeChoicesAreValidatedAndFrozenInPlan(t *testing.T) {
	modules := []installer.Module{{ID: "prewalk", Operations: []installer.Operation{
		{Kind: "copy", Root: "codex", Target: "agents/prewalk_executor.toml", Source: "executor.toml"},
		{Kind: "native-config", Root: "codex", Target: "config.toml"},
	}}}
	e, _ := testEngine(t, modules, map[string]string{"executor.toml": "developer_instructions = 'keep'\nmodel_provider = 'legacy'\n"})
	path := filepath.Join(e.CodexHome, "config.toml")
	writeFixture(t, path, "model_provider = 'legacy'\nmodel_catalog_json = '/old/catalog.json'\n[model_providers.legacy]\nbase_url = 'http://127.0.0.1:9999'\n", 0600)
	choices := installer.DefaultModelChoices()
	choices["prewalk_executor"] = installer.ModelChoice{Model: "gpt-5.6-sol", Effort: "high"}
	p, err := e.BuildPlanWithModels([]string{"prewalk"}, choices)
	if err != nil {
		t.Fatal(err)
	}
	choices["prewalk_executor"] = installer.ModelChoice{Model: "foreign/model", Effort: "ultra"}
	applyPlan(t, e, p)
	c := readTOML(t, path)
	if c["model"] != "gpt-6-astra" || c["model_reasoning_effort"] != "medium" || c["forced_login_method"] != "chatgpt" {
		t.Fatalf("wrong native root: %#v", c)
	}
	for _, key := range []string{"model_provider", "model_providers", "model_catalog_json"} {
		if c[key] != nil {
			t.Fatalf("provider state remains: %s", key)
		}
	}
	if c["features"].(map[string]any)["multi_agent_v2"] != true {
		t.Fatal("V2 missing")
	}
	r := readTOML(t, filepath.Join(e.CodexHome, "agents/prewalk_executor.toml"))
	if r["model"] != "gpt-5.6-sol" || r["model_reasoning_effort"] != "high" || r["model_reasoning_summary"] != nil || r["model_provider"] != nil || r["developer_instructions"] != "keep" {
		t.Fatalf("choice not applied/preserved: %#v", r)
	}
	if _, err = e.BuildPlanWithModels([]string{"prewalk"}, choices); err == nil {
		t.Fatal("foreign model accepted")
	}
	if _, err = e.BuildPlanWithModels([]string{"prewalk"}, map[string]installer.ModelChoice{"unknown": {Model: "gpt-6-astra", Effort: "medium"}}); err == nil {
		t.Fatal("unknown role accepted")
	}
	if _, err = e.BuildPlanWithModels([]string{"prewalk"}, map[string]installer.ModelChoice{"default": {Model: "gpt-5.3-codex-spark", Effort: "ultra"}}); err == nil {
		t.Fatal("unsupported Spark effort accepted")
	}
}

func TestNativeOptionsDoNotExposeMutableCache(t *testing.T) {
	options := installer.NativeModelOptions()
	options[0].Efforts[0] = "corrupt"
	if installer.NativeModelOptions()[0].Efforts[0] == "corrupt" {
		t.Fatal("options share mutable storage")
	}
}
