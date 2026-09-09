package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestReadinessRejectsModifiedPayload(t *testing.T) {
	const name, content = "script.sh", "#!/bin/sh\nexit 0\n"
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{name: &fstest.MapFile{Data: []byte(content)}}}
	path := filepath.Join(e.CodexHome, name)
	for _, tc := range []struct {
		data  string
		mode  os.FileMode
		valid bool
	}{
		{content, 0700, true},
		{"", 0700, false},
		{"#!/bin/sh\nexit 1\n", 0700, false},
		{content, 0600, false},
	} {
		if err := os.WriteFile(path, []byte(tc.data), tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := e.checkInstalledPayload(name, path, false, true); (err == nil) != tc.valid {
			t.Fatalf("payload %q mode %o: %v", tc.data, tc.mode, err)
		}
	}
	if checkInstalledQlty(path) == nil {
		t.Fatal("unverified Qlty accepted")
	}
}

func TestReadinessChecksSkillState(t *testing.T) {
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{"skills/example/SKILL.md": &fstest.MapFile{Data: []byte("skill")}}}
	op := Operation{Kind: "skills-state", Source: "skills", Root: "codex", Target: "skills", Enabled: true}
	for _, enabled := range []bool{true, false} {
		data := fmt.Sprintf("[[skills.config]]\npath=%q\nenabled=%t\n", filepath.Join(e.CodexHome, "skills/example/SKILL.md"), enabled)
		if err := os.WriteFile(filepath.Join(e.CodexHome, "config.toml"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := e.checkInstalledOperation("fixture", op); (err == nil) != enabled {
			t.Fatalf("enabled=%t: %v", enabled, err)
		}
	}
}

func TestReadinessFilesRequiresEveryRegularChild(t *testing.T) {
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{"source/child": &fstest.MapFile{Data: []byte("payload")}}}
	op := Operation{Kind: "tree", Root: "codex", Target: "tree", Source: "source"}
	path := filepath.Join(e.CodexHome, "tree", "child")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation("fixture", op) == nil {
		t.Fatal("empty tree accepted")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation("fixture", op) == nil {
		t.Fatal("directory replacing file accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.checkInstalledOperation("fixture", op); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "foreign"), path); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation("fixture", op) == nil {
		t.Fatal("symlink child accepted")
	}
	op.Kind, op.Target = "copy", "tree"
	if e.checkInstalledOperation("fixture", op) == nil {
		t.Fatal("copy destination directory accepted")
	}
}

func TestReadinessUsesInstalledAccountChoices(t *testing.T) {
	const configName = "config.toml"
	const accountModel = "account-specific"
	e := &Engine{CodexHome: t.TempDir(), Modules: []Module{{ID: "base", Operations: []Operation{{Kind: "native-config", Root: "codex", Target: configName}}}}}
	status := &AccountStatus{Models: []ModelOption{{Model: accountModel, Efforts: []string{"high"}}}}
	path := filepath.Join(e.CodexHome, configName)
	write := func(generic string) {
		t.Helper()
		data := "model='account-specific'\nmodel_reasoning_effort='high'\nforced_login_method='chatgpt'\n[features]\nmulti_agent=true\nmulti_agent_v2=true\n[agents]\ndefault_subagent_model='" + generic + "'\ndefault_subagent_reasoning_effort='high'\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(accountModel)
	r, err := e.CheckReadiness([]string{"base"}, status)
	if err != nil || !r.Ready() {
		t.Fatalf("non-default available choice rejected: %#v %v", r, err)
	}
	before, _ := os.ReadFile(path)
	write("unavailable")
	r, err = e.CheckReadiness([]string{"base"}, status)
	if err != nil || r.Ready() {
		t.Fatal("unavailable default accepted")
	}
	write(accountModel)
	r, err = e.CheckReadiness([]string{"base"}, nil)
	if err != nil || r.Ready() {
		t.Fatal("missing account accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("readiness changed configuration")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), configName), path); err != nil {
		t.Fatal(err)
	}
	r, err = e.CheckReadiness([]string{"base"}, status)
	if err != nil || r.Ready() {
		t.Fatal("symlink config accepted")
	}
}
