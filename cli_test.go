package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cliDryRun = "--dry-run"
const cliInstallDeps = "--install-deps"

func mockCLIAccount(t *testing.T) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!" + python + "\n" + `import json, os, sys
for line in sys.stdin:
    message=json.loads(line)
    if "id" not in message: continue
    method=message["method"]
    if method=="initialize": result={}
    elif method=="account/read": result={"account":None if os.environ.get("MOCK_NO_LOGIN")=="1" else {"type":"chatgpt"}}
    elif method=="model/list": result={"data":[{"model":"account-only","defaultReasoningEffort":"medium","isDefault":True,"supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}]}
    elif method=="hooks/list": result={"data":[]}
    elif method=="config/read": result={"config":{"features":{"hooks":False}}}
    else: raise RuntimeError("unexpected method")
    print(json.dumps({"id":message["id"],"result":result}),flush=True)
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCLIAccountPreviewInstallAndReadOnlyCheck(t *testing.T) {
	mockCLIAccount(t)
	home := t.TempDir()
	args := []string{"--home", home, "--modules", "base"}
	if err := runCLIArgs(append(args, cliDryRun)); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".codex", "config.toml")
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote configuration")
	}
	if err := runCLIArgs(append(args, "--yes")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(before), "account-only") {
		t.Fatalf("account model not installed: %s %v", before, err)
	}
	if err := runCLIArgs(append(args, "--check")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(config)
	if string(before) != string(after) {
		t.Fatal("check changed config")
	}
	t.Setenv("MOCK_NO_LOGIN", "1")
	if err := runCLIArgs(append(args, "--check")); err == nil {
		t.Fatal("missing login reported ready")
	}
}

func TestCLIDependenciesRequireIndependentConsent(t *testing.T) {
	mockCLIAccount(t)
	home := t.TempDir()
	args := []string{"--home", home, "--modules", "zg"}
	if err := runCLIArgs(append(args, cliDryRun)); err != nil {
		t.Fatal(err)
	}
	if err := runCLIArgs(append(args, "--yes")); err == nil || !strings.Contains(err.Error(), cliInstallDeps) {
		t.Fatalf("missing dependency consent: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preview/yes installed without dependency consent: %v %v", entries, err)
	}
	if err := runCLIArgs(append(args, cliInstallDeps)); err == nil {
		t.Fatal("dependency install without yes accepted")
	}
	if err := runCLIArgs(append(args, "--yes", cliInstallDeps, cliDryRun)); err == nil {
		t.Fatal("dry-run dependency installation accepted")
	}
}

func TestCLICheckRejectsEmptyModuleSelection(t *testing.T) {
	mockCLIAccount(t)
	if err := runCLIArgs([]string{"--home", t.TempDir(), "--modules", ",, ", "--check"}); err == nil {
		t.Fatal("empty selection reported ready")
	}
}
