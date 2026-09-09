package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestReadinessManagedInstructions(t *testing.T) {
	const source = "policy.md"
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{source: &fstest.MapFile{Data: []byte("managed policy")}}}
	target := filepath.Join(e.CodexHome, "instructions")
	for _, kind := range []string{"append", "developer-instructions"} {
		op := Operation{Kind: kind, Source: source, Root: "codex", Target: "instructions"}
		for _, valid := range []bool{false, true} {
			checkManagedInstructionsFixture(t, e, target, op, valid)
		}
	}
}

func checkManagedInstructionsFixture(t *testing.T, e *Engine, target string, op Operation, valid bool) {
	t.Helper()
	data := []byte("user instructions")
	if valid {
		var err error
		data, err = managedBlock(data, []byte("managed policy"), "fixture")
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Kind == "developer-instructions" {
		data = []byte(fmt.Sprintf("developer_instructions=%q\n", data))
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.checkInstalledOperation("fixture", op, false); (err == nil) != valid {
		t.Fatalf("%s valid=%t: %v", op.Kind, valid, err)
	}
}

func TestReadinessMergedConfigPreservesAccountChoiceAndExtraFields(t *testing.T) {
	const source = "profile.toml"
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{source: &fstest.MapFile{Data: []byte("model='preset'\n[tui]\ntheme='managed'\n")}}}
	op := Operation{Kind: "merge", Source: source, Root: "codex", Target: "profile.toml"}
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{"", false},
		{"[tui]\ntheme='other'\n", false},
		{"model='account-choice'\ncustom='keep'\n[tui]\ntheme='managed'\n", true},
	} {
		if err := os.WriteFile(filepath.Join(e.CodexHome, op.Target), []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := e.checkInstalledOperation("fixture", op, false); (err == nil) != tc.valid {
			t.Fatalf("config %q: %v", tc.data, err)
		}
	}
}

func TestReadinessPrewalkSettingsAndRole(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings")
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{"", false},
		{"{}", false},
		{`{"worktrees":true,"max_workers":5}`, false},
		{`{"worktrees":true,"max_workers":2.5}`, false},
		{`{"worktrees":true,"max_workers":4,"quality":"true"}`, false},
		{`{"worktrees":false,"max_workers":2,"quality":false,"custom":"keep"}`, true},
	} {
		if err := os.WriteFile(target, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkInstalledPrewalkSettings(target); (err == nil) != tc.valid {
			t.Fatalf("settings %s: %v", tc.data, err)
		}
	}
	for _, instruction := range []string{"", "user-customized role instructions"} {
		if err := os.WriteFile(target, []byte(fmt.Sprintf("developer_instructions=%q\n", instruction)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkInstalledRole(target); (err == nil) != (instruction != "") {
			t.Fatalf("role %q: %v", instruction, err)
		}
	}
}

func TestReadinessPrewalkConfigPreservesPermissions(t *testing.T) {
	const source = "prewalk.toml"
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{source: &fstest.MapFile{Data: []byte("sandbox_mode='workspace-write'\napproval_policy='on-request'\napprovals_reviewer='auto_review'\n[features]\nhooks=true\n")}}}
	op := Operation{Kind: "prewalk-config", Source: source, Root: "codex", Target: "config.toml"}
	target := filepath.Join(e.CodexHome, op.Target)
	for _, valid := range []bool{false, true} {
		data := "sandbox_mode='read-only'\napproval_policy='never'\napprovals_reviewer='user'\n[features]\nhooks=true\n"
		if valid {
			data += fmt.Sprintf("[sandbox_workspace_write]\nwritable_roots=[%q,%q]\n", "/custom", filepath.Join(e.CodexHome, "worktrees/prewalk"))
		}
		if err := os.WriteFile(target, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := e.checkInstalledOperation("fixture", op, false); (err == nil) != valid {
			t.Fatalf("valid=%t: %v", valid, err)
		}
		after, err := os.ReadFile(target)
		if err != nil || string(after) != data {
			t.Fatal("check mutated configuration")
		}
	}
}

func TestReadinessBaseHooksAllowSelectedOverride(t *testing.T) {
	const source = "config/base.toml"
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{source: &fstest.MapFile{Data: []byte("[features]\nhooks=false\n")}}}
	op := Operation{Kind: "merge", Source: source, Root: "codex", Target: "config.toml"}
	if err := os.WriteFile(filepath.Join(e.CodexHome, op.Target), []byte("model_provider='openai'\n[features]\nhooks=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.checkInstalledOperation("base", op, false); err == nil {
		t.Fatal("base hooks override accepted without a hooks module")
	}
	if err := e.checkInstalledOperation("base", op, true); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessProviderMatchesNativeAccount(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.toml")
	for _, provider := range []string{"openai", "", "external"} {
		if err := os.WriteFile(target, []byte(fmt.Sprintf("model_provider=%q\n", provider)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := installedModelConfig(target); (err == nil) != (provider != "external") {
			t.Fatalf("provider %q: %v", provider, err)
		}
	}
}
