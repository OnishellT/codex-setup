package installer

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestZGManagedPreflight(t *testing.T) {
	assets := fstest.MapFS{
		"modules.json":   &fstest.MapFile{Data: []byte(`[{"id":"zg","operations":[{"kind":"merge","source":"config/zg.toml","root":"codex","target":"config.toml"}]}]`)},
		"config/zg.toml": &fstest.MapFile{Data: []byte("[mcp_servers.zvec_grep]\nenabled = true\n")},
		zgHelper:         &fstest.MapFile{Data: []byte("// fixture helper")},
	}
	e, err := New(assets, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	// A PATH executable must never satisfy a managed zg requirement.
	bin := t.TempDir()
	write(filepath.Join(bin, "node"), "#!/bin/sh\necho unexpected\n", 0755)
	t.Setenv("PATH", bin)
	if e.checkZG() == nil {
		t.Fatal("unmanaged runtime accepted")
	}
	node := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v22.23.2; elif [ \"$2\" = --version ]; then echo 10.9.8; elif [ \"$4\" = check ] || [ \"$4\" = apply ]; then echo \"${ZG_PATCH:-patched}\"; else echo \"${ZG_NATIVE:-ready}\"; fi\n"
	write(e.managedZGNode(), node, 0755)
	write(e.managedZGNPM(), "npm", 0600)
	pkg := zgPackage(e.managedZGPackages())
	write(filepath.Join(pkg, "package.json"), `{"name":"@zvec/zvec-grep","version":"0.2.1","bin":{"zg":"dist/cli/index.js"}}`, 0600)
	write(filepath.Join(pkg, zgCLI), "cli", 0600)
	write(filepath.Join(pkg, "dist/daemon/watch-manager.js"), "watch", 0600)
	if _, err := e.BuildPlan([]string{"zg"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZG_PATCH", "original")
	if e.checkZG() == nil {
		t.Fatal("unpatched package accepted")
	}
	t.Setenv("ZG_PATCH", "patched")
	t.Setenv("ZG_NATIVE", "broken")
	if e.checkZG() == nil {
		t.Fatal("missing native component accepted")
	}
	t.Setenv("ZG_NATIVE", "ready")
	write(filepath.Join(pkg, "package.json"), `{"name":"@zvec/zvec-grep","version":"0.2.2"}`, 0600)
	if e.checkZG() == nil {
		t.Fatal("wrong version accepted")
	}
	if err := e.installZG(io.Discard, io.Discard); err == nil {
		t.Fatal("invalid existing package overwritten")
	}
	data, _ := os.ReadFile(filepath.Join(pkg, "package.json"))
	if !strings.Contains(string(data), "0.2.2") {
		t.Fatal("existing package changed")
	}
}

func TestZGPackageRejectsBadIntegrity(t *testing.T) {
	mockNodeDownload(t, []byte("foreign"), false)
	if _, err := downloadZGPackage(); err == nil {
		t.Fatal("unverified tarball accepted")
	}
}

func TestZGConfigExpandsArgumentPaths(t *testing.T) {
	value := map[string]any{"args": []any{"{{CODEX_HOME}}/cli.js", "server"}}
	expandConfigPaths(value, "/home/path with spaces")
	if value["args"].([]any)[0] != "/home/path with spaces/cli.js" {
		t.Fatal(value)
	}
}

// Explicit opt-in: real public npm install into a disposable home; no models or indices.
func TestZGOfficialInstall(t *testing.T) {
	if os.Getenv("CODEX_SETUP_ZG_NETWORK_TEST") != "1" {
		t.Skip("set CODEX_SETUP_ZG_NETWORK_TEST=1")
	}
	data, err := os.ReadFile(os.Getenv("CODEX_SETUP_NODE_ARCHIVE"))
	if err != nil {
		t.Fatal(err)
	}
	old := zgNodeClient
	t.Cleanup(func() { zgNodeClient = old })
	zgNodeClient = &http.Client{Transport: rtkRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "nodejs.org" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	e, err := New(os.DirFS("../../payload"), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := e.PlanDependencies([]string{"zg"})
	if err != nil || !contains(dependencies.Missing, depZG) || len(dependencies.Commands) != 0 {
		t.Fatalf("expected only private install, no host package commands: %#v %v", dependencies, err)
	}
	if err := e.InstallDependencies(dependencies, nil, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if err := e.checkZG(); err != nil {
		t.Fatal(err)
	}
	if err := e.installZG(io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	plan, err := e.BuildPlan([]string{"zg"})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if strings.HasSuffix(change.Path, "config.toml") && (bytes.Contains(change.data, []byte("{{CODEX_HOME}}")) || !bytes.Contains(change.data, []byte(e.managedZGNode()))) {
			t.Fatalf("config does not bind managed runtime: %s", change.data)
		}
	}
}
