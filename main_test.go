package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"codex-setup/internal/installer"
	"github.com/pelletier/go-toml/v2"
)

const testFallbackModel = "gpt-5.6-luna"

func testEngine(t *testing.T) *installer.Engine {
	t.Helper()
	data, err := fs.Sub(assets, "payload")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise generated paths with spaces and apostrophes too.
	home := filepath.Join(t.TempDir(), "test user's home")
	e, err := installer.New(data, home, "")
	if err != nil {
		t.Fatal(err)
	}
	// Packaging tests exercise configuration only. Managed Qlty bootstrap is
	// covered by installer tests and the opt-in official installation check.
	for i := range e.Modules {
		var operations []installer.Operation
		for _, op := range e.Modules[i].Operations {
			if op.Kind != "qlty-install" {
				operations = append(operations, op)
			}
		}
		e.Modules[i].Operations = operations
	}
	return e
}

func expandedCodexHomePath(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestPayloadContainsNoMachineState(t *testing.T) {
	err := fs.WalkDir(assets, "payload", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("symlink packaged: %s", p)
		}
		for _, forbidden := range []string{"auth.json", "history.jsonl", ".env", "__pycache__", ".git", "credentials.json"} {
			if d.Name() == forbidden {
				t.Errorf("private/runtime path packaged: %s", p)
			}
		}
		if strings.HasSuffix(p, ".sqlite") || strings.HasSuffix(p, ".pyc") {
			t.Errorf("runtime state packaged: %s", p)
		}
		if !d.IsDir() {
			b, er := fs.ReadFile(assets, p)
			if er != nil {
				return er
			}
			if bytes.Contains(b, []byte("/home/dev/")) || bytes.Contains(b, []byte("/nix/store/")) {
				t.Errorf("source-machine path: %s", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackagedHooksInstallOnlyPonytailSkills(t *testing.T) {
	e := testEngine(t)
	ids := []string{"rtk", "ponytail"}
	p, err := e.BuildPlan(ids)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(e.Home); !os.IsNotExist(err) {
		t.Fatalf("preview touched destination: %v", err)
	}
	result, err := e.Apply(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed < 15 {
		t.Fatalf("unexpected payload size: %d", result.Changed)
	}
	again, err := e.BuildPlan(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Changes) != 0 {
		t.Fatalf("not idempotent: %v", again.Changes)
	}
	want := map[string]bool{
		"ponytail": true, "ponytail-audit": true, "ponytail-debt": true,
		"ponytail-gain": true, "ponytail-help": true, "ponytail-review": true,
	}
	entries, err := os.ReadDir(filepath.Join(e.CodexHome, "skills"))
	if err != nil || len(entries) != len(want) {
		t.Fatalf("expected only six Ponytail skills: %v, %v", entries, err)
	}
	for _, entry := range entries {
		if !want[entry.Name()] {
			t.Errorf("unexpected installed skill: %s", entry.Name())
		}
		if _, err := os.Stat(filepath.Join(e.CodexHome, "skills", entry.Name(), "SKILL.md")); err != nil {
			t.Error(err)
		}
	}
	if err := fs.WalkDir(assets, "payload", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "SKILL.md" && (!strings.HasPrefix(name, "payload/skills/") || (!want[filepath.Base(filepath.Dir(name))] && filepath.Base(filepath.Dir(name)) != "prewalk")) {
			t.Errorf("unexpected skill packaged: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedContextHandoff(t *testing.T) {
	e := testEngine(t)
	p, err := e.BuildPlan([]string{"context-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(e.CodexHome, "integrations", "handoff", "context.py")
	for _, name := range []string{
		contextPath,
		filepath.Join(e.CodexHome, "integrations", "handoff", "settings.json"),
		filepath.Join(e.CodexHome, "skills", "handoff", "SKILL.md"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("context-handoff payload missing %s: %v", name, err)
		}
	}
	configData, err := os.ReadFile(filepath.Join(e.CodexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = toml.Unmarshal(configData, &config); err != nil || config["features"].(map[string]any)["hooks"] != true {
		t.Fatalf("hooks feature not enabled: %v, %#v", err, config)
	}
	hooks, err := os.ReadFile(filepath.Join(e.CodexHome, "hooks.json"))
	if err != nil || !strings.Contains(string(hooks), "[codex-setup:context-handoff]") || strings.Contains(string(hooks), "{{CODEX_HOME_SHELL}}") {
		t.Fatalf("invalid context-handoff hooks: %v\n%s", err, hooks)
	}
	settingsPath := filepath.Join(e.CodexHome, "integrations", "handoff", "settings.json")
	custom := []byte("{\"warning_percent\":60,\"critical_percent\":80,\"handoff_max_chars\":9000}\n")
	if err = os.WriteFile(settingsPath, custom, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := e.BuildPlan([]string{"context-handoff"})
	if err != nil || len(again.Changes) != 0 {
		t.Fatalf("context-handoff not idempotent: %v, %v", again, err)
	}
	if got, _ := os.ReadFile(settingsPath); !bytes.Equal(got, custom) {
		t.Fatal("custom handoff settings were overwritten")
	}

	project := filepath.Join(t.TempDir(), "plain project")
	if err = os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-B", contextPath, "new", "--cwd", project)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+e.CodexHome)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	if err = json.Unmarshal(out, &created); err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(created["path"])
	if err != nil {
		t.Fatal(err)
	}
	artifact = bytes.ReplaceAll(artifact, []byte("REPLACE:"), []byte("Recorded:"))
	if err = os.WriteFile(created["path"], artifact, 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("python3", "-B", contextPath, "validate", created["path"])
	cmd.Env = append(os.Environ(), "CODEX_HOME="+e.CodexHome)
	if out, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("installed handoff validation failed: %v\n%s", err, out)
	}
}

func TestPackagedPrewalkDefaultsToNativeCodex(t *testing.T) {
	e := testEngine(t)
	p, err := e.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(e.Home); !os.IsNotExist(err) {
		t.Fatal("preview touched destination")
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	readConfig := func(name string) map[string]any {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(e.CodexHome, name))
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := toml.Unmarshal(b, &config); err != nil {
			t.Fatal(err)
		}
		return config
	}
	config := readConfig("config.toml")
	if config["sandbox_mode"] != "workspace-write" || config["approval_policy"] != "on-request" || config["approvals_reviewer"] != "auto_review" {
		t.Fatalf("Prewalk must install native writer permissions: %#v", config)
	}
	sandboxWrite, ok := config["sandbox_workspace_write"].(map[string]any)
	if !ok {
		t.Fatalf("Prewalk writable roots table missing: %#v", config)
	}
	root := filepath.Join(e.CodexHome, "worktrees", "prewalk")
	roots, ok := sandboxWrite["writable_roots"].([]any)
	if !ok || len(roots) != 1 || roots[0] != root {
		t.Fatalf("Prewalk writable root = %#v, want %s", sandboxWrite["writable_roots"], root)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("Prewalk worktree root missing/private: %v, %v", info, err)
	}
	dispatcher := config["developer_instructions"].(string)
	if !strings.Contains(dispatcher, "automatically") || config["model"] != "gpt-6-astra" {
		t.Fatal("Prewalk must be automatic with the native preset")
	}
	if strings.Contains(dispatcher, "{{CODEX_HOME_SHELL}}") || !strings.Contains(dispatcher, expandedCodexHomePath(e.CodexHome)+"/skills/prewalk/SKILL.md") {
		t.Fatal("Prewalk dispatcher must point to the expanded installed skill path")
	}
	dispatcherSource, err := fs.ReadFile(assets, "payload/prewalk.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcherSource) >= 2048 || len(dispatcher) >= 2048 {
		t.Fatalf("Prewalk dispatcher is not compact: source=%d installed=%d", len(dispatcherSource), len(dispatcher))
	}
	for _, detailed := range []string{"terminal completed state", "wait wake-up is not proof of completion", "quality.py setup", "Qlty scan in the integration worktree", "least two explorers concurrently"} {
		if strings.Contains(dispatcher, detailed) {
			t.Fatalf("Prewalk dispatcher contains detailed policy: %s", detailed)
		}
	}
	skillPath := filepath.Join(e.CodexHome, "skills", "prewalk", "SKILL.md")
	skillFile, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("Prewalk skill was not installed: %v", err)
	}
	skill := string(skillFile)
	expandedCodexHome := expandedCodexHomePath(e.CodexHome)
	if strings.Contains(skill, "{{CODEX_HOME_SHELL}}") || !strings.Contains(skill, expandedCodexHome) {
		t.Fatalf("Prewalk skill did not expand its portable CODEX_HOME path: %s", skill)
	}
	for _, required := range []string{"name: prewalk", "terminal completed state", "wait wake-up is not proof of completion", "do not finish the parent turn", "prepare --repo", "quality.py setup", "quality.py scan", "Qlty scan in the integration worktree", "writing project files or delegating writers", "never create a nested repository", "never invent an identity", "least two explorers concurrently"} {
		if !strings.Contains(skill, required) {
			t.Fatalf("installed Prewalk is missing the completion guard: %s", required)
		}
	}
	skillsTable, ok := config["skills"].(map[string]any)
	if !ok {
		t.Fatalf("Prewalk skill registration missing: %#v", config["skills"])
	}
	registered := false
	if entries, ok := skillsTable["config"].([]any); ok {
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if ok && entry["path"] == skillPath && entry["enabled"] == true {
				registered = true
			}
		}
	}
	if !registered {
		t.Fatalf("Prewalk skill is not registered enabled: %#v", skillsTable["config"])
	}
	role := readConfig("agents/prewalk_executor.toml")
	if role["name"] != "prewalk_executor" || role["model"] != "gpt-5.3-codex-spark" || role["model_provider"] != nil || role["model_reasoning_effort"] != "medium" || role["agents"].(map[string]any)["enabled"] != false {
		t.Fatalf("invalid executor: %#v", role)
	}
	if !strings.Contains(role["developer_instructions"].(string), "usa el ejecutable `apply_patch` mediante exec_command") {
		t.Fatal("executor must include the command-line apply_patch fallback")
	}
	if config["model_provider"] != nil || config["model_catalog_json"] != nil || config["model_providers"] != nil {
		t.Fatalf("native installation must not configure a custom provider or catalog: %#v", config)
	}
	if _, err := os.Stat(filepath.Join(e.CodexHome, "model-catalogs")); !os.IsNotExist(err) {
		t.Fatalf("native installation unexpectedly installed a custom catalog: %v", err)
	}
	agents := config["agents"].(map[string]any)
	if agents["max_concurrent_threads_per_session"] != int64(4) || agents["default_subagent_model"] != "gpt-5.3-codex-spark" || agents["default_subagent_reasoning_effort"] != "medium" {
		t.Fatalf("invalid native agent limit: %#v", config["agents"])
	}
	for name, want := range map[string][2]string{
		"explorer":          {"gpt-5.3-codex-spark", "medium"},
		"fallback_explorer": {testFallbackModel, "medium"},
		"critical_explorer": {"gpt-5.3-codex-spark", "medium"},
		"fallback_executor": {testFallbackModel, "medium"},
	} {
		got := readConfig("agents/" + name + ".toml")
		if got["model"] != want[0] || got["model_reasoning_effort"] != want[1] || got["agents"].(map[string]any)["enabled"] != false {
			t.Fatalf("invalid native %s role: %#v", name, got)
		}
	}
	fallbackExplorer := readConfig("agents/fallback_explorer.toml")
	if fallbackExplorer["model_provider"] != nil {
		t.Fatalf("native fallback explorer must not configure a model provider: %#v", fallbackExplorer)
	}
	reviewer := readConfig("agents/engineering_reviewer.toml")
	if reviewer["name"] != "engineering_reviewer" || reviewer["model"] != "gpt-5.6-sol" || reviewer["model_reasoning_effort"] != "xhigh" || reviewer["sandbox_mode"] != "read-only" || reviewer["agents"].(map[string]any)["enabled"] != false {
		t.Fatalf("invalid independent reviewer: %#v", reviewer)
	}
	if reviewer["approval_policy"] != nil || reviewer["mcp_servers"] != nil {
		t.Fatal("reviewer must not change approvals or MCP configuration")
	}
	for _, requirement := range []string{"AGENTS.override.md", "reportes deterministas de Qlty", "complejidad ciclomática/cognitiva", "no escribas", "no delegues", "Propósito", "Corrección", "Reglas", "Rendimiento", "Diseño", "Seguridad", "Pruebas", "Integración", "`verified`", "`finding`", "`N/A`", "`not verified`", "P0-P3", "revisión incompleta"} {
		if !strings.Contains(reviewer["developer_instructions"].(string), requirement) {
			t.Fatalf("reviewer missing requirement: %s", requirement)
		}
	}
	if !strings.Contains(skill, "engineering_reviewer") || strings.Contains(dispatcher, "engineering_reviewer") {
		t.Fatal("Prewalk skill must invoke the independent reviewer without expanding the dispatcher")
	}
	features := config["features"].(map[string]any)
	if features["hooks"] != true {
		t.Fatalf("Prewalk hooks feature not enabled: %#v", features)
	}
	hooks, err := os.ReadFile(filepath.Join(e.CodexHome, "hooks.json"))
	if err != nil || !strings.Contains(string(hooks), "[codex-setup:prewalk] Guarding reviewer context") || strings.Contains(string(hooks), "{{CODEX_HOME_SHELL}}") {
		t.Fatalf("invalid installed Prewalk hooks: %v\n%s", err, hooks)
	}
	reviewerFile, err := os.ReadFile(filepath.Join(e.CodexHome, "agents", "engineering_reviewer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reviewer["developer_instructions"].(string), "{{CODEX_HOME_SHELL}}") || strings.Contains(string(reviewerFile), "[codex-setup:prewalk-review]") {
		t.Fatal("reviewer role must not retain a managed role-scoped guard")
	}
	for _, name := range []string{"work.config.toml", "personal.config.toml"} {
		if _, err := os.Stat(filepath.Join(e.CodexHome, name)); !os.IsNotExist(err) {
			t.Errorf("Prewalk unexpectedly installed %s", name)
		}
	}
	for _, name := range []string{"hooks.json", "review_guard.py", "worktrees.py", "quality.py"} {
		if _, err := os.Stat(filepath.Join(e.CodexHome, "integrations", "prewalk", name)); err != nil {
			t.Errorf("Prewalk integration missing %s: %v", name, err)
		}
	}
	settingsData, err := os.ReadFile(filepath.Join(e.CodexHome, "integrations", "prewalk", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prewalkSettings map[string]any
	if err = json.Unmarshal(settingsData, &prewalkSettings); err != nil || prewalkSettings["max_workers"] != float64(4) {
		t.Fatalf("invalid Prewalk worker limit: %v, %#v", err, prewalkSettings)
	}
	// Exercise the installed (embedded) helper, not just the source tree.
	project := filepath.Join(t.TempDir(), "new project")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(e.CodexHome, "integrations", "prewalk", "worktrees.py")
	cmd := exec.Command("python3", "-B", helper, "prepare", "--repo", project)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=Prewalk test", "GIT_CONFIG_KEY_1=user.email", "GIT_CONFIG_VALUE_1=test@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installed Git preparation failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", project, "ls-tree", "-r", "--name-only", "HEAD").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != ".gitignore" {
		t.Fatalf("initial commit must contain only .gitignore: %v\n%s", err, out)
	}
	p, err = e.BuildPlan([]string{"prewalk"})
	if err != nil || len(p.Changes) != 0 {
		t.Fatalf("not idempotent: %v", err)
	}
	result, err := e.Apply(p, nil)
	if err != nil || result.Changed != 0 || result.BackupDir != "" {
		t.Fatalf("unchanged reinstall created a backup: %+v %v", result, err)
	}
}

func TestPayloadIncludesManagedQlty(t *testing.T) {
	data, err := fs.ReadFile(assets, "payload/modules.json")
	if err != nil {
		t.Fatal(err)
	}
	var modules []installer.Module
	if err := json.Unmarshal(data, &modules); err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		for _, op := range module.Operations {
			if module.ID == "prewalk" && op.Kind == "qlty-install" && op.Root == "codex" && op.Target == "integrations/prewalk/bin/qlty" {
				return
			}
		}
	}
	t.Fatal("Prewalk must require the managed Qlty runtime")
}

func TestPackagedFallbackExecutorMigratesGeneratedInstructions(t *testing.T) {
	e := testEngine(t)
	data, err := fs.ReadFile(assets, "payload/agents/fallback_executor.toml")
	if err != nil {
		t.Fatal(err)
	}
	var role map[string]any
	if err = toml.Unmarshal(data, &role); err != nil {
		t.Fatal(err)
	}
	wanted := role["developer_instructions"].(string)
	var old []string
	for _, paragraph := range strings.Split(wanted, "\n\n") {
		if !strings.HasPrefix(paragraph, "Como writer de Prewalk,") && !strings.HasPrefix(paragraph, "Haz el commit de los cambios aprobados") {
			old = append(old, paragraph)
		}
	}
	role["developer_instructions"] = strings.Join(old, "\n\n")
	role["model"] = "preserved-fallback-model"
	data, err = toml.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(e.CodexHome, "agents", "fallback_executor.toml")
	if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := e.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = toml.Unmarshal(data, &role); err != nil {
		t.Fatal(err)
	}
	if role["developer_instructions"] != wanted || role["model"] != testFallbackModel {
		t.Fatal("fallback migration did not apply the native preset and current instructions")
	}
}

func TestPackagedPrewalkPreservesWritableRootsAndPermissionChoices(t *testing.T) {
	e := testEngine(t)
	if err := os.MkdirAll(e.CodexHome, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(e.CodexHome, "config.toml")
	config := "sandbox_mode = 'danger-full-access'\napproval_policy = 'never'\napprovals_reviewer = 'user'\n[sandbox_workspace_write]\nwritable_roots = ['/existing/root']\n"
	if err := os.WriteFile(configPath, []byte(config), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0755); err != nil {
		t.Fatal(err)
	}
	p, err := e.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["sandbox_mode"] != "danger-full-access" || got["approval_policy"] != "never" || got["approvals_reviewer"] != "user" {
		t.Fatalf("custom permission choices changed: %#v", got)
	}
	sandbox := got["sandbox_workspace_write"].(map[string]any)
	root := filepath.Join(e.CodexHome, "worktrees", "prewalk")
	if !reflect.DeepEqual(sandbox["writable_roots"], []any{"/existing/root", root}) {
		t.Fatalf("writable roots = %#v", sandbox["writable_roots"])
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private worktree root = %v, %v", info, err)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("config mode changed unexpectedly: %v, %v", info, err)
	}
}

func TestPackagedCustomNativeRoleChoice(t *testing.T) {
	e := testEngine(t)
	p, err := e.BuildPlanWithModels([]string{"base", "prewalk"}, map[string]installer.ModelChoice{
		"prewalk_executor": {Model: "gpt-5.6-sol", Effort: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.CodexHome, "agents/prewalk_executor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var role map[string]any
	if err = toml.Unmarshal(b, &role); err != nil {
		t.Fatal(err)
	}
	if role["model"] != "gpt-5.6-sol" || role["model_reasoning_effort"] != "high" || role["model_reasoning_summary"] != nil {
		t.Fatalf("custom choice lost: %#v", role)
	}
}

func TestPackagedFallbackChoiceWithoutLuna(t *testing.T) {
	e := testEngine(t)
	const available = "gpt-5.6-sol"
	status := &installer.AccountStatus{DefaultModel: available, Models: []installer.ModelOption{{Model: available, Efforts: []string{"high"}}}}
	requested := map[string]installer.ModelChoice{
		"fallback_explorer": {Model: available, Effort: "high"},
		"fallback_executor": {Model: available, Effort: "high"},
	}
	choices, _, err := installer.ResolveAccountChoices(status, requested)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.BuildPlanWithModels([]string{"prewalk"}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	for name, choice := range requested {
		data, err := os.ReadFile(filepath.Join(e.CodexHome, "agents", name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		var role map[string]any
		if err := toml.Unmarshal(data, &role); err != nil {
			t.Fatal(err)
		}
		if role["model"] != choice.Model || role["model_reasoning_effort"] != choice.Effort {
			t.Fatalf("explicit fallback lost: %#v", role)
		}
	}
}

func TestPackagedZGIsOptInAndPreserving(t *testing.T) {
	// Reuse the packaged operations under a fixture ID to isolate merging from
	// runtime provisioning. TestZGOfficialInstall exercises real pinned runtimes.
	const fixtureID = "zg-fixture"
	realNode, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	e := testEngine(t)
	runtimeDir := filepath.Join(e.CodexHome, "integrations", "zg")
	node := filepath.Join(runtimeDir, "node-v22.23.2", "bin", "node")
	pkg := filepath.Join(runtimeDir, "packages-v0.2.1", "node_modules", "@zvec", "zvec-grep")
	for name, content := range map[string]string{
		filepath.Join(pkg, "package.json"):                                                            `{"name":"@zvec/zvec-grep","version":"0.2.1","bin":{"zg":"dist/cli/index.js"}}`,
		filepath.Join(pkg, "dist", "cli", "index.js"):                                                 "cli",
		filepath.Join(pkg, "dist", "daemon", "watch-manager.js"):                                      "watch",
		filepath.Join(runtimeDir, "node-v22.23.2", "lib", "node_modules", "npm", "bin", "npm-cli.js"): "npm",
		node: "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v22.23.2; elif [ \"$2\" = --version ]; then echo 10.9.8; elif [ \"$4\" = check ]; then echo patched; else echo ready; fi\n",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	var zg installer.Module
	for i, module := range e.Modules {
		if module.ID == "zg" {
			zg = module
			e.Modules[i].ID = fixtureID
			break
		}
	}
	if zg.ID == "" || zg.Default {
		t.Fatalf("zg must be an available opt-in module: %#v", zg)
	}

	if err := os.MkdirAll(e.CodexHome, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(e.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"keep\"\napproval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n[mcp_servers.other]\ncommand = \"other\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(e.CodexHome, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("User instructions stay here.\n"), 0600); err != nil {
		t.Fatal(err)
	}

	p, err := e.BuildPlan([]string{"prewalk", fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := toml.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if config["model"] != "gpt-6-astra" || !strings.Contains(config["developer_instructions"].(string), "automatically") {
		t.Fatalf("zg changed existing or Prewalk configuration: %#v", config)
	}
	mcp := config["mcp_servers"].(map[string]any)
	if mcp["other"].(map[string]any)["command"] != "other" {
		t.Fatalf("zg replaced an existing MCP server: %#v", mcp)
	}
	server := mcp["zvec_grep"].(map[string]any)
	if server["command"] != node || server["enabled"] != true || server["required"] != false || server["default_tools_approval_mode"] != "auto" || server["startup_timeout_sec"] != int64(30) || server["tool_timeout_sec"] != int64(120) {
		t.Fatalf("unexpected zg server configuration: %#v", server)
	}
	args := server["args"].([]any)
	tools := server["enabled_tools"].([]any)
	if len(args) != 5 || args[0] != filepath.Join(pkg, "dist", "cli", "index.js") || strings.Join([]string{args[1].(string), args[2].(string), args[3].(string), args[4].(string)}, " ") != "server --stdio --mcp-toolset agent" || len(tools) != 1 || tools[0] != "zvec_grep_search" {
		t.Fatalf("zg exposes unexpected command or tools: %#v", server)
	}
	if _, found := server["env"]; found {
		t.Fatalf("zg config must not package environment or credentials: %#v", server)
	}
	if config["approval_policy"] != "on-request" || config["sandbox_mode"] != "workspace-write" || server["tools"] != nil {
		t.Fatalf("zg changed approvals or added per-tool authorization: %#v", config)
	}

	agents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "User instructions stay here.") || !strings.Contains(string(agents), "<!-- codex-setup:"+fixtureID+" -->") {
		t.Fatalf("zg did not preserve or manage AGENTS.md: %s", agents)
	}
	for _, guidance := range []string{"freshness: \"wait_for_fresh\"", "possibly_stale", "timeout", "error", "live file", "continue with `rg`"} {
		if !strings.Contains(string(agents), guidance) {
			t.Fatalf("zg is missing freshness fallback guidance: %s", guidance)
		}
	}
	readme, err := os.ReadFile(filepath.Join(e.CodexHome, "integrations", "zg", "README.md"))
	if err != nil || !strings.Contains(string(readme), "@zvec/zvec-grep@0.2.1") {
		t.Fatalf("zg integration documentation was not copied: %v\n%s", err, readme)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".local", "bin", "zg")); !os.IsNotExist(err) {
		t.Fatalf("zg module must not install a binary: %v", err)
	}
	helper := filepath.Join(e.CodexHome, "integrations", "zg", "pr86-watch-manager.mjs")
	if _, err := os.Stat(helper); err != nil {
		t.Fatalf("zg patch helper was not installed: %v", err)
	}
	cmd := exec.Command(realNode, "--test", filepath.Join(e.CodexHome, "integrations", "zg", "test-pr86.mjs"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installed zg patch helper tests: %v\n%s", err, out)
	}

	// Reapplying the selected module enables an older disabled installation,
	// without replacing Prewalk or unrelated configuration.
	server["enabled"] = false
	disabled, err := toml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, disabled, 0600); err != nil {
		t.Fatal(err)
	}
	p, err = e.BuildPlan([]string{fixtureID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = map[string]any{}
	if err := toml.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	server = config["mcp_servers"].(map[string]any)["zvec_grep"].(map[string]any)
	if server["enabled"] != true || config["model"] != "gpt-6-astra" || !strings.Contains(config["developer_instructions"].(string), "automatically") {
		t.Fatalf("zg reapply did not enable only its managed configuration: %#v", config)
	}
	if config["approval_policy"] != "on-request" || config["sandbox_mode"] != "workspace-write" || server["default_tools_approval_mode"] != "auto" || server["tools"] != nil {
		t.Fatalf("zg reapply changed approvals: %#v", config)
	}

	p, err = e.BuildPlan([]string{"prewalk", fixtureID})
	if err != nil || len(p.Changes) != 0 {
		t.Fatalf("zg install is not idempotent: %v, %v", p, err)
	}
}

func TestPackagedPanelInstallation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("panel requires Linux")
	}
	e := testEngine(t)
	p, err := e.BuildPlan([]string{"panel"})
	if err != nil {
		t.Skipf("panel system dependencies unavailable: %v", err)
	}
	if _, err = os.Stat(e.Home); !os.IsNotExist(err) {
		t.Fatal("preview touched destination")
	}
	if _, err = e.Apply(p, nil); err != nil {
		t.Fatal(err)
	}
	p, err = e.BuildPlan([]string{"panel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 0 {
		t.Fatalf("panel not idempotent: %v", p.Changes)
	}
	launcher := filepath.Join(e.Home, ".local", "bin", "codex-panel")
	cmd := exec.Command(launcher, "--help")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("portable launcher: %s: %v", out, err)
	}
	// Exercise generated shell quoting and native wrapper without a model call.
	fakeCLI := filepath.Join(e.Home, "fake official codex")
	if err = os.WriteFile(fakeCLI, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(filepath.Join(e.Home, ".local", "bin", "codex-with-panel"), "exec", "--json", "prompt with spaces", "")
	cmd.Env = append(os.Environ(), "CODEX_PANEL_REAL_CODEX="+fakeCLI)
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "<exec>\n<--json>\n<prompt with spaces>\n<>\n" {
		t.Fatalf("native wrapper changed arguments: %v %s", err, out)
	}
	cmd = exec.Command("python3", "-B", "-m", "unittest", "discover", "-v")
	cmd.Dir = filepath.Join(e.Home, ".local", "share", "codex-panel")
	cmd.Env = append(os.Environ(), "CODEX_PANEL_QUOTA_OFFLINE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installed panel tests: %v\n%s", err, out)
	}
	t.Logf("installed panel suite:\n%s", out)
	status := &installer.AccountStatus{Models: []installer.ModelOption{{Model: "gpt-6-astra", Efforts: []string{"medium"}}, {Model: "gpt-5.3-codex-spark", Efforts: []string{"medium"}}}}
	ready, err := e.CheckReadiness([]string{"panel"}, status)
	if err != nil || !ready.Ready() {
		t.Fatalf("installed panel not ready: %+v %v", ready, err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	ready, err = e.CheckReadiness([]string{"panel"}, status)
	if err != nil || ready.Ready() {
		t.Fatal("modified panel launcher reported ready")
	}
}
