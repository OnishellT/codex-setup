package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex-setup/internal/installer"
	"github.com/pelletier/go-toml/v2"
)

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
	return e
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
		if !entry.IsDir() && entry.Name() == "SKILL.md" && (!strings.HasPrefix(name, "payload/skills/") || !want[filepath.Base(filepath.Dir(name))]) {
			t.Errorf("unexpected skill packaged: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedPrewalkIsPortableAndConfigDriven(t *testing.T) {
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
	if !strings.Contains(config["developer_instructions"].(string), "Apply it automatically") || config["model"] != nil {
		t.Fatal("Prewalk must be automatic without replacing the primary model")
	}
	for _, required := range []string{"terminal completed state", "wait wake-up is not proof of completion", "do not finish the parent turn"} {
		if !strings.Contains(config["developer_instructions"].(string), required) {
			t.Fatalf("installed Prewalk is missing the completion guard: %s", required)
		}
	}
	role := readConfig("agents/prewalk_executor.toml")
	if role["name"] != "prewalk_executor" || role["model"] != "gpt-5.6-terra" || role["agents"].(map[string]any)["enabled"] != false {
		t.Fatalf("invalid executor: %#v", role)
	}
	for _, name := range []string{"hooks.json", "skills", "work.config.toml", "personal.config.toml"} {
		if _, err := os.Stat(filepath.Join(e.CodexHome, name)); !os.IsNotExist(err) {
			t.Errorf("Prewalk unexpectedly installed %s", name)
		}
	}
	// The executor is user-owned after installation, including future providers.
	role["model"] = "custom-executor"
	role["model_provider"] = "custom-provider"
	custom, err := toml.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.CodexHome, "agents/prewalk_executor.toml"), custom, 0600); err != nil {
		t.Fatal(err)
	}
	p, err = e.BuildPlan([]string{"prewalk"})
	if err != nil || len(p.Changes) != 0 {
		t.Fatalf("not idempotent: %v, %v", p, err)
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
}
