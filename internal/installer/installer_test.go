package installer_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"codex-setup/internal/installer"
	"github.com/pelletier/go-toml/v2"
)

func testAssets(t *testing.T, modules []installer.Module, files map[string]string) fstest.MapFS {
	t.Helper()
	manifest, err := json.Marshal(modules)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"modules.json": &fstest.MapFile{Data: manifest}}
	for name, content := range files {
		assets[name] = &fstest.MapFile{Data: []byte(content), Mode: 0644}
	}
	return assets
}

func testEngine(t *testing.T, modules []installer.Module, files map[string]string) (*installer.Engine, fstest.MapFS) {
	t.Helper()
	home := t.TempDir()
	assets := testAssets(t, modules, files)
	engine, err := installer.New(assets, home, filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	return engine, assets
}

func writeFixture(t *testing.T, filename, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// Make mode assertions independent of the invoking process's umask.
	if err := os.Chmod(filename, mode); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, filename string) []byte {
	t.Helper()
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertContent(t *testing.T, filename, want string) {
	t.Helper()
	if got := string(readFixture(t, filename)); got != want {
		t.Errorf("%s content = %q, want %q", filename, got, want)
	}
}

func assertMode(t *testing.T, filename string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s permissions = %04o, want %04o", filename, got, want)
	}
}

func assertAbsent(t *testing.T, filename string) {
	t.Helper()
	if _, err := os.Lstat(filename); !os.IsNotExist(err) {
		t.Errorf("%s should not exist; Lstat error = %v", filename, err)
	}
}

func buildPlan(t *testing.T, engine *installer.Engine, ids ...string) *installer.Plan {
	t.Helper()
	plan, err := engine.BuildPlan(ids)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func applyPlan(t *testing.T, engine *installer.Engine, plan *installer.Plan) installer.Result {
	t.Helper()
	result, err := engine.Apply(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != len(plan.Changes) {
		t.Errorf("Changed = %d, want %d", result.Changed, len(plan.Changes))
	}
	return result
}

func assertIdempotent(t *testing.T, engine *installer.Engine, ids ...string) {
	t.Helper()
	plan := buildPlan(t, engine, ids...)
	if len(plan.Changes) != 0 {
		t.Fatalf("second plan has changes: %+v", plan.Changes)
	}
	result := applyPlan(t, engine, plan)
	if result.BackupDir != "" {
		t.Errorf("no-op Apply created a backup: %s", result.BackupDir)
	}
}

func readTOML(t *testing.T, filename string) map[string]any {
	t.Helper()
	var config map[string]any
	if err := toml.Unmarshal(readFixture(t, filename), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestResolveDependencyOrdering(t *testing.T) {
	engine, _ := testEngine(t, []installer.Module{
		{ID: "app", Depends: []string{"left", "right"}},
		{ID: "right", Depends: []string{"base"}},
		{ID: "base"},
		{ID: "left", Depends: []string{"base"}},
	}, nil)
	modules, err := engine.Resolve([]string{"app", "right", "app", "base"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, module := range modules {
		ids = append(ids, module.ID)
	}
	if want := []string{"base", "left", "right", "app"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("resolved IDs = %v, want %v (dependencies first, no duplicates)", ids, want)
	}
	plan := buildPlan(t, engine, "app")
	if !reflect.DeepEqual(plan.Modules, modules) {
		t.Errorf("BuildPlan modules = %+v, want %+v", plan.Modules, modules)
	}
	if _, err := engine.Resolve([]string{"missing"}); err == nil {
		t.Error("Resolve accepted an unknown selection")
	}
	if _, err := engine.BuildPlan(nil); err == nil {
		t.Error("BuildPlan accepted an empty selection")
	}
}

func TestNewRejectsInvalidDependencyGraphs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		modules []installer.Module
	}{
		{"self cycle", []installer.Module{{ID: "a", Depends: []string{"a"}}}},
		{"indirect cycle", []installer.Module{{ID: "a", Depends: []string{"b"}}, {ID: "b", Depends: []string{"c"}}, {ID: "c", Depends: []string{"a"}}}},
		{"unknown dependency", []installer.Module{{ID: "a", Depends: []string{"missing"}}}},
		{"duplicate ID", []installer.Module{{ID: "a"}, {ID: "a"}}},
		{"empty ID", []installer.Module{{ID: ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := installer.New(testAssets(t, tc.modules, nil), home, filepath.Join(home, ".codex")); err == nil {
				t.Fatal("New accepted an invalid manifest")
			}
		})
	}
}

func TestTOMLMergePreservesUnrelatedKeysAndIsIdempotent(t *testing.T) {
	engine, _ := testEngine(t, []installer.Module{
		{ID: "base", Operations: []installer.Operation{{Kind: "merge", Root: "codex", Target: "config.toml", Source: "base.toml"}}},
		{ID: "profile", Depends: []string{"base"}, Operations: []installer.Operation{{Kind: "merge", Root: "codex", Target: "config.toml", Source: "profile.toml"}}},
	}, map[string]string{
		"base.toml":    "model = 'base'\n[features]\nmanaged = true\n[profiles.work]\nmodel = 'base'\n",
		"profile.toml": "model = 'selected'\n[profiles.work]\nmodel = 'selected'\nreasoning = 'high'\n",
	})
	filename := filepath.Join(engine.CodexHome, "config.toml")
	original := "# user comment\nmodel = 'old'\nunrelated = 'keep'\n[features]\ncustom = false\n[profiles.work]\ncustom = 42\n[profiles.personal]\nmodel = 'personal'\n"
	writeFixture(t, filename, original, 0640)
	plan := buildPlan(t, engine, "profile")
	if len(plan.Changes) != 1 || plan.Changes[0].Path != filename {
		t.Fatalf("expected one consolidated config change, got %+v", plan.Changes)
	}
	assertContent(t, filename, original) // Preview must not mutate the destination.
	applyPlan(t, engine, plan)
	want := map[string]any{
		"model": "selected", "unrelated": "keep",
		"features": map[string]any{"custom": false, "managed": true},
		"profiles": map[string]any{
			"work":     map[string]any{"custom": int64(42), "model": "selected", "reasoning": "high"},
			"personal": map[string]any{"model": "personal"},
		},
	}
	if got := readTOML(t, filename); !reflect.DeepEqual(got, want) {
		t.Errorf("merged config = %#v, want %#v", got, want)
	}
	assertMode(t, filename, 0640)
	before := readFixture(t, filename)
	assertIdempotent(t, engine, "profile")
	if !bytes.Equal(before, readFixture(t, filename)) {
		t.Error("idempotent merge changed config bytes")
	}
}

func TestDeveloperInstructionsPreserveUserConfigAndUpdate(t *testing.T) {
	engine, assets := testEngine(t, []installer.Module{{ID: "prewalk", Operations: []installer.Operation{
		{Kind: "developer-instructions", Root: "codex", Target: "config.toml", Source: "prewalk.md"},
	}}}, map[string]string{"prewalk.md": "Automatic Prewalk v1"})
	filename := filepath.Join(engine.CodexHome, "config.toml")
	writeFixture(t, filename, "model = 'user-model'\nsandbox_mode = 'workspace-write'\napproval_policy = 'on-request'\napprovals_reviewer = 'auto_review'\ndeveloper_instructions = 'Keep my instructions.'\n[features]\nhooks = false\n", 0600)
	applyPlan(t, engine, buildPlan(t, engine, "prewalk"))
	want := "Keep my instructions.\n\n<!-- codex-setup:prewalk -->\nAutomatic Prewalk v1\n<!-- /codex-setup:prewalk -->\n"
	got := readTOML(t, filename)
	if got["developer_instructions"] != want || got["model"] != "user-model" ||
		got["sandbox_mode"] != "workspace-write" || got["approval_policy"] != "on-request" ||
		got["approvals_reviewer"] != "auto_review" || got["features"].(map[string]any)["hooks"] != false {
		t.Fatalf("unexpected configuration: %#v", got)
	}
	assertIdempotent(t, engine, "prewalk")
	assets["prewalk.md"].Data = []byte("Automatic Prewalk v2")
	applyPlan(t, engine, buildPlan(t, engine, "prewalk"))
	if readTOML(t, filename)["developer_instructions"] != strings.Replace(want, "v1", "v2", 1) {
		t.Fatal("update did not preserve user instructions")
	}
	assertIdempotent(t, engine, "prewalk")
}

func TestDeveloperInstructionsRejectInvalidExistingConfig(t *testing.T) {
	for _, content := range []string{"broken = [", "developer_instructions = 42", "developer_instructions = '<!-- codex-setup:prewalk -->'"} {
		engine, _ := testEngine(t, []installer.Module{{ID: "prewalk", Operations: []installer.Operation{
			{Kind: "developer-instructions", Root: "codex", Target: "config.toml", Source: "prewalk.md"},
		}}}, map[string]string{"prewalk.md": "Automatic Prewalk"})
		filename := filepath.Join(engine.CodexHome, "config.toml")
		writeFixture(t, filename, content, 0600)
		if _, err := engine.BuildPlan([]string{"prewalk"}); err == nil {
			t.Fatalf("accepted invalid configuration: %s", content)
		}
		assertContent(t, filename, content)
	}
}

func TestInstructionsManagedAppendAndUpdateAreIdempotent(t *testing.T) {
	engine, assets := testEngine(t, []installer.Module{{ID: "instructions", Operations: []installer.Operation{
		{Kind: "append", Root: "codex", Target: "AGENTS.md", Source: "instructions.md"},
	}}}, map[string]string{"instructions.md": "Managed instructions v1.\n"})
	filename := filepath.Join(engine.CodexHome, "AGENTS.md")
	writeFixture(t, filename, "User instructions.\n", 0600)
	applyPlan(t, engine, buildPlan(t, engine, "instructions"))
	want := "User instructions.\n\n<!-- codex-setup:instructions -->\nManaged instructions v1.\n<!-- /codex-setup:instructions -->\n"
	assertContent(t, filename, want)
	assertIdempotent(t, engine, "instructions")
	writeFixture(t, filename, want+"\nUser footer.\n", 0600)
	assets["instructions.md"].Data = []byte("Managed instructions v2.\n")
	applyPlan(t, engine, buildPlan(t, engine, "instructions"))
	assertContent(t, filename, strings.Replace(want, "v1", "v2", 1)+"\nUser footer.\n")
	assertIdempotent(t, engine, "instructions")
}

func TestInstructionsRejectMalformedManagedBlocks(t *testing.T) {
	start := "<!-- codex-setup:instructions -->"
	end := "<!-- /codex-setup:instructions -->"
	for _, original := range []string{start, end, end + "\n" + start, start + "\n" + start + "\n" + end, start + "\n" + end + "\n" + end} {
		t.Run(original, func(t *testing.T) {
			engine, _ := testEngine(t, []installer.Module{{ID: "instructions", Operations: []installer.Operation{
				{Kind: "append", Root: "codex", Target: "AGENTS.md", Source: "instructions.md"},
			}}}, map[string]string{"instructions.md": "Managed content"})
			filename := filepath.Join(engine.CodexHome, "AGENTS.md")
			writeFixture(t, filename, original, 0600)
			if _, err := engine.BuildPlan([]string{"instructions"}); err == nil {
				t.Error("BuildPlan accepted malformed managed markers")
			}
			assertContent(t, filename, original)
		})
	}
}

func TestHooksStatePreservesForeignHooksAndMixedGroups(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user's home with spaces")
	modules := []installer.Module{{ID: "rtk", Operations: []installer.Operation{{Kind: "hooks-state", Root: "codex", Target: "ignored", Source: "rtk-hooks.json"}}}}
	assets := testAssets(t, modules, map[string]string{
		"rtk-hooks.json": `{
  "description": "module template is not copied over user metadata",
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "python3 {{CODEX_HOME_SHELL}}/integrations/rtk/hook.py", "statusMessage": "[codex-setup:rtk] RTK"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "python3 {{CODEX_HOME_SHELL}}/integrations/rtk/hook.py", "statusMessage": "[codex-setup:rtk] RTK prompt"}]}]
  }
}`,
	})
	engine, err := installer.New(assets, home, filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	hooksFile := filepath.Join(engine.CodexHome, "hooks.json")
	writeFixture(t, hooksFile, `{
  "description": "user metadata",
  "hooks": {
    "PreToolUse": [{"matcher":"Bash","hooks":[
      {"type":"command","command":"user-hook","statusMessage":"user hook"},
      {"type":"command","command":"stale-rtk","statusMessage":"[codex-setup:rtk] old"}
    ]}],
    "Stop": [{"hooks":[{"type":"command","command":"keep-stop","statusMessage":"keep"}]}]
  }
}`, 0600)
	configFile := filepath.Join(engine.CodexHome, "config.toml")
	writeFixture(t, configFile, "model = 'keep-me'\n[profiles.work]\nmodel = 'work-model'\n[features]\nplugins = false\nhooks = false\n", 0600)

	applyPlan(t, engine, buildPlan(t, engine, "rtk"))
	var hooks map[string]any
	if err = json.Unmarshal(readFixture(t, hooksFile), &hooks); err != nil {
		t.Fatal(err)
	}
	if hooks["description"] != "user metadata" {
		t.Fatalf("foreign top-level metadata overwritten: %#v", hooks)
	}
	preGroups := hooks["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(preGroups) != 2 {
		t.Fatalf("unexpected PreToolUse groups: %#v", preGroups)
	}
	foreignHandlers := preGroups[0].(map[string]any)["hooks"].([]any)
	if len(foreignHandlers) != 1 || foreignHandlers[0].(map[string]any)["command"] != "user-hook" {
		t.Fatalf("mixed hook group did not retain foreign handler: %#v", foreignHandlers)
	}
	managedHandlers := preGroups[1].(map[string]any)["hooks"].([]any)
	command := managedHandlers[0].(map[string]any)["command"].(string)
	if !strings.Contains(command, "'"+strings.ReplaceAll(engine.CodexHome, "'", "'\"'\"'")+"'/integrations/rtk/hook.py") {
		t.Fatalf("CODEX_HOME was not POSIX quoted safely: %q", command)
	}
	if _, found := hooks["hooks"].(map[string]any)["Stop"]; !found {
		t.Fatal("foreign event removed")
	}
	config := readTOML(t, configFile)
	if config["model"] != "keep-me" || config["profiles"].(map[string]any)["work"].(map[string]any)["model"] != "work-model" {
		t.Fatalf("model/profile changed while enabling hooks: %#v", config)
	}
	features := config["features"].(map[string]any)
	if features["hooks"] != true || features["plugins"] != false {
		t.Fatalf("wrong feature mutation: %#v", features)
	}
	assertIdempotent(t, engine, "rtk")
}

func TestHooksStateRejectsUnmarkedTemplate(t *testing.T) {
	engine, _ := testEngine(t, []installer.Module{{ID: "rtk", Operations: []installer.Operation{{Kind: "hooks-state", Source: "hooks.json"}}}}, map[string]string{
		"hooks.json": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo unsafe","statusMessage":"not owned"}]}]}}`,
	})
	if _, err := engine.BuildPlan([]string{"rtk"}); err == nil {
		t.Fatal("accepted a template without a managed ownership marker")
	}
}

func TestBuildPlanRejectsTraversal(t *testing.T) {
	for _, kind := range []string{"copy", "tree", "skills-state"} {
		for _, target := range []string{"../escape", "nested/../../escape", "/absolute/escape", ""} {
			t.Run(kind+"/"+target, func(t *testing.T) {
				source := "payload"
				if kind == "copy" {
					source = "payload/SKILL.md"
				}
				engine, _ := testEngine(t, []installer.Module{{ID: "bad", Operations: []installer.Operation{
					{Kind: kind, Root: "codex", Target: target, Source: source},
				}}}, map[string]string{"payload/SKILL.md": "content"})
				if _, err := engine.BuildPlan([]string{"bad"}); err == nil {
					t.Error("BuildPlan accepted an invalid target")
				}
				entries, err := os.ReadDir(engine.Home)
				if err != nil || len(entries) != 0 {
					t.Errorf("preview mutated temporary home: entries=%v, err=%v", entries, err)
				}
			})
		}
	}
}

func TestSymlinkDestinationsAreRefused(t *testing.T) {
	for _, stage := range []string{"preview", "apply"} {
		for _, ancestor := range []bool{false, true} {
			name := stage + "/file"
			if ancestor {
				name = stage + "/ancestor"
			}
			t.Run(name, func(t *testing.T) {
				engine, _ := testEngine(t, []installer.Module{{ID: "copy", Operations: []installer.Operation{
					{Kind: "copy", Root: "codex", Target: "nested/file.txt", Source: "file.txt"},
				}}}, map[string]string{"file.txt": "installer content"})
				outside := t.TempDir()
				outsideFile := filepath.Join(outside, "file.txt")
				writeFixture(t, outsideFile, "unrelated content", 0600)
				var plan *installer.Plan
				if stage == "apply" {
					plan = buildPlan(t, engine, "copy")
				}
				link, destination := filepath.Join(engine.CodexHome, "nested", "file.txt"), outsideFile
				if ancestor {
					link, destination = filepath.Dir(link), outside
				}
				if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
				if stage == "preview" {
					if _, err := engine.BuildPlan([]string{"copy"}); err == nil {
						t.Error("BuildPlan accepted a symlink destination")
					}
				} else {
					result, err := engine.Apply(plan, nil)
					if err == nil || result.Changed != 0 || result.BackupDir != "" {
						t.Errorf("Apply = %+v, %v; want refusal before mutation", result, err)
					}
				}
				assertContent(t, outsideFile, "unrelated content")
				if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("destination symlink was modified: %v", err)
				}
			})
		}
	}
}

func TestApplyRefusesStalePreviewBeforeAnyWrites(t *testing.T) {
	for _, mutation := range []string{"content", "permissions", "removed", "created"} {
		t.Run(mutation, func(t *testing.T) {
			engine, _ := testEngine(t, []installer.Module{{ID: "files", Operations: []installer.Operation{
				{Kind: "copy", Root: "codex", Target: "a.txt", Source: "new.txt"},
				{Kind: "copy", Root: "codex", Target: "b.txt", Source: "new.txt"},
			}}}, map[string]string{"new.txt": "new"})
			first, second := filepath.Join(engine.CodexHome, "a.txt"), filepath.Join(engine.CodexHome, "b.txt")
			writeFixture(t, first, "original first", 0600)
			if mutation != "created" {
				writeFixture(t, second, "original second", 0600)
			}
			plan := buildPlan(t, engine, "files")
			switch mutation {
			case "content", "created":
				writeFixture(t, second, "concurrent edit", 0600)
			case "permissions":
				if err := os.Chmod(second, 0640); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if err := os.Remove(second); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			result, err := engine.Apply(plan, func(string) { called = true })
			if err == nil || result.Changed != 0 || result.BackupDir != "" || called {
				t.Errorf("stale Apply = %+v, %v, progress=%v; want preflight refusal", result, err, called)
			}
			assertContent(t, first, "original first")
			switch mutation {
			case "content", "created":
				assertContent(t, second, "concurrent edit")
			case "permissions":
				assertContent(t, second, "original second")
				assertMode(t, second, 0640)
			case "removed":
				assertAbsent(t, second)
			}
			assertAbsent(t, filepath.Join(engine.Home, ".local", "state", "codex-setup", "backups"))
		})
	}
}

func TestApplyBackupsAndFileModes(t *testing.T) {
	engine, _ := testEngine(t, []installer.Module{{ID: "files", Operations: []installer.Operation{
		{Kind: "copy", Root: "codex", Target: "existing.txt", Source: "copy.txt"},
		{Kind: "copy", Root: "codex", Target: "new.txt", Source: "copy.txt"},
		{Kind: "merge", Root: "codex", Target: "config.toml", Source: "config.toml"},
		{Kind: "append", Root: "codex", Target: "AGENTS.md", Source: "instructions.md"},
		{Kind: "tree", Root: "bin", Target: "tools", Source: "tree"},
	}}}, map[string]string{
		"copy.txt": "replacement", "config.toml": "model = 'test'\n", "instructions.md": "Instructions",
		"tree/run.sh": "#!/bin/sh\nexit 0\n", "tree/readme.txt": "Documentation",
	})
	existing := filepath.Join(engine.CodexHome, "existing.txt")
	writeFixture(t, existing, "private original", 0640)
	plan := buildPlan(t, engine, "files")
	result := applyPlan(t, engine, plan)
	backupRoot := filepath.Join(engine.Home, ".local", "state", "codex-setup", "backups")
	if result.BackupDir == "" || filepath.Dir(result.BackupDir) != backupRoot {
		t.Fatalf("backup directory = %q, want a child of %s", result.BackupDir, backupRoot)
	}
	assertMode(t, backupRoot, 0700)
	assertMode(t, result.BackupDir, 0700)
	manifestPath := filepath.Join(result.BackupDir, "manifest.json")
	assertMode(t, manifestPath, 0600)
	var journal []struct {
		Path    string `json:"path"`
		File    string `json:"file"`
		Existed bool   `json:"existed"`
		Mode    uint32 `json:"mode"`
	}
	if err := json.Unmarshal(readFixture(t, manifestPath), &journal); err != nil {
		t.Fatal(err)
	}
	if len(journal) != len(plan.Changes) {
		t.Fatalf("backup journal entries = %d, want %d", len(journal), len(plan.Changes))
	}
	backups := 0
	for i, entry := range journal {
		if entry.Path != plan.Changes[i].Path {
			t.Errorf("journal path = %s, want %s", entry.Path, plan.Changes[i].Path)
		}
		if entry.Path == existing {
			backups++
			if !entry.Existed || entry.Mode != 0640 || entry.File == "" || filepath.Base(entry.File) != entry.File {
				t.Fatalf("invalid existing-file backup entry: %+v", entry)
			}
			backup := filepath.Join(result.BackupDir, entry.File)
			assertContent(t, backup, "private original")
			assertMode(t, backup, 0600)
		} else if entry.Existed || entry.File != "" {
			t.Errorf("new file unexpectedly has an original backup: %+v", entry)
		}
	}
	if backups != 1 {
		t.Errorf("original-file backups = %d, want 1", backups)
	}
	assertContent(t, existing, "replacement")
	assertMode(t, existing, 0640)
	assertMode(t, filepath.Join(engine.CodexHome, "new.txt"), 0644)
	assertMode(t, filepath.Join(engine.CodexHome, "config.toml"), 0600)
	assertMode(t, filepath.Join(engine.CodexHome, "AGENTS.md"), 0600)
	assertMode(t, filepath.Join(engine.Home, ".local", "bin", "tools", "run.sh"), 0755)
	assertMode(t, filepath.Join(engine.Home, ".local", "bin", "tools", "readme.txt"), 0644)
	assertIdempotent(t, engine, "files")
}

func TestSkillsStateMixedEnableDisablePreservesUnrelatedEntries(t *testing.T) {
	engine, _ := testEngine(t, []installer.Module{{ID: "skills", Operations: []installer.Operation{
		{Kind: "skills-state", Root: "skills", Target: "enabled", Source: "enabled", Enabled: true},
		{Kind: "skills-state", Root: "skills", Target: "disabled", Source: "disabled", Enabled: false},
	}}}, map[string]string{
		"enabled/existing/SKILL.md": "enabled", "enabled/new/SKILL.md": "new enabled",
		"disabled/existing/SKILL.md": "disabled", "disabled/new/SKILL.md": "new disabled",
		"enabled/README.md": "not a skill", "disabled/notes.txt": "not a skill",
	})
	skillsRoot := filepath.Join(engine.CodexHome, "skills")
	enabledDirectory := filepath.Join(skillsRoot, "enabled", "existing")
	disabledFile := filepath.Join(skillsRoot, "disabled", "existing", "SKILL.md")
	unrelated := filepath.Join(skillsRoot, "unrelated", "SKILL.md")
	config := map[string]any{
		"model": "keep-model",
		"skills": map[string]any{
			"custom_setting": "keep-setting",
			"config": []map[string]any{
				{"path": enabledDirectory, "enabled": false, "note": "keep-enabled-note"},
				{"path": disabledFile, "enabled": true, "note": "keep-disabled-note"},
				{"path": unrelated, "enabled": false, "note": "keep-unrelated-note"},
			},
		},
	}
	original, err := toml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(engine.CodexHome, "config.toml")
	writeFixture(t, filename, string(original), 0600)
	plan := buildPlan(t, engine, "skills")
	if len(plan.Changes) != 1 || plan.Changes[0].Path != filename {
		t.Fatalf("skills-state should change only config.toml: %+v", plan.Changes)
	}
	applyPlan(t, engine, plan)
	var got struct {
		Model  string `toml:"model"`
		Skills struct {
			Custom string           `toml:"custom_setting"`
			Config []map[string]any `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(readFixture(t, filename), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "keep-model" || got.Skills.Custom != "keep-setting" {
		t.Errorf("unrelated TOML settings changed: %+v", got)
	}
	want := map[string]map[string]any{
		enabledDirectory: {"path": enabledDirectory, "enabled": true, "note": "keep-enabled-note"},
		disabledFile:     {"path": disabledFile, "enabled": false, "note": "keep-disabled-note"},
		unrelated:        {"path": unrelated, "enabled": false, "note": "keep-unrelated-note"},
	}
	for _, state := range []struct {
		group   string
		enabled bool
	}{{"enabled", true}, {"disabled", false}} {
		filename := filepath.Join(skillsRoot, state.group, "new", "SKILL.md")
		want[filename] = map[string]any{"path": filename, "enabled": state.enabled}
	}
	if len(got.Skills.Config) != len(want) {
		t.Fatalf("skills entries = %+v, want exactly %d entries", got.Skills.Config, len(want))
	}
	for _, entry := range got.Skills.Config {
		filename, ok := entry["path"].(string)
		if !ok || !reflect.DeepEqual(entry, want[filename]) {
			t.Errorf("unexpected or duplicate skills entry: %#v; want %#v", entry, want[filename])
		}
		delete(want, filename)
	}
	if len(want) != 0 {
		t.Errorf("missing skills entries: %#v", want)
	}
	assertIdempotent(t, engine, "skills")
}

func TestApplyRollsBackAfterProgressIntroducesSecondPathConflict(t *testing.T) {
	for _, existed := range []bool{true, false} {
		name := "restore existing first file"
		if !existed {
			name = "remove newly created first file"
		}
		t.Run(name, func(t *testing.T) {
			engine, _ := testEngine(t, []installer.Module{{ID: "files", Operations: []installer.Operation{
				{Kind: "copy", Root: "codex", Target: "a-first.txt", Source: "new.txt"},
				{Kind: "copy", Root: "codex", Target: "b-second.txt", Source: "new.txt"},
			}}}, map[string]string{"new.txt": "installed content"})
			first := filepath.Join(engine.CodexHome, "a-first.txt")
			second := filepath.Join(engine.CodexHome, "b-second.txt")
			sibling := filepath.Join(engine.CodexHome, "unrelated.txt")
			writeFixture(t, sibling, "unrelated sibling", 0600)
			if existed {
				writeFixture(t, first, "original first", 0640)
			}
			plan := buildPlan(t, engine, "files")
			if len(plan.Changes) != 2 || plan.Changes[0].Path != first || plan.Changes[1].Path != second {
				t.Fatalf("unexpected apply order: %+v", plan.Changes)
			}
			callbacks := 0
			conflictContent := filepath.Join(second, "unrelated-child.txt")
			result, err := engine.Apply(plan, func(message string) {
				callbacks++
				if callbacks == 2 {
					if !strings.HasSuffix(message, second) {
						t.Errorf("second callback = %q, want second target %s", message, second)
					}
					assertContent(t, first, "installed content")
					writeFixture(t, conflictContent, "concurrent unrelated content", 0600)
				}
			})
			if err == nil {
				t.Fatal("Apply succeeded despite second-path conflict")
			}
			if callbacks != 2 || result.Changed != 0 || result.BackupDir == "" {
				t.Errorf("rollback result = %+v, callbacks=%d, err=%v", result, callbacks, err)
			}
			if existed {
				assertContent(t, first, "original first")
				assertMode(t, first, 0640)
			} else {
				assertAbsent(t, first)
			}
			assertContent(t, conflictContent, "concurrent unrelated content")
			assertContent(t, sibling, "unrelated sibling")
			entries, readErr := os.ReadDir(engine.CodexHome)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".codex-setup-") {
					t.Errorf("rollback leaked temporary file %s", entry.Name())
				}
			}
		})
	}
}
