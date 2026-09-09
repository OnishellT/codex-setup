package installer

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestReadinessFilesRequiresEveryRegularChild(t *testing.T) {
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{"source/child": &fstest.MapFile{Data: []byte("payload")}}}
	op := Operation{Kind: "tree", Root: "codex", Target: "tree", Source: "source"}
	path := filepath.Join(e.CodexHome, "tree", "child")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation(op) == nil {
		t.Fatal("empty tree accepted")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation(op) == nil {
		t.Fatal("directory replacing file accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("installed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.checkInstalledOperation(op); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "foreign"), path); err != nil {
		t.Fatal(err)
	}
	if e.checkInstalledOperation(op) == nil {
		t.Fatal("symlink child accepted")
	}
	op.Kind, op.Target = "copy", "tree"
	if e.checkInstalledOperation(op) == nil {
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
		data := "model='account-specific'\nmodel_reasoning_effort='high'\n[agents]\ndefault_subagent_model='" + generic + "'\ndefault_subagent_reasoning_effort='high'\n"
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
