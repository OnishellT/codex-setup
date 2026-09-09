package installer

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestQltyOfficialInstall(t *testing.T) {
	if os.Getenv("CODEX_SETUP_QLTY_NETWORK_TEST") != "1" {
		t.Skip("set CODEX_SETUP_QLTY_NETWORK_TEST=1")
	}
	e := qltyTestEngine(t)
	p, err := e.PlanDependencies([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) != 0 {
		t.Skip("install native prerequisites separately; this test never runs a package manager")
	}
	if err := e.InstallDependencies(p, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !e.qltyAvailable() {
		t.Fatal("installed Qlty failed integrity check")
	}
	if out, err := exec.Command(e.managedQlty(), "--version").CombinedOutput(); err != nil || !strings.Contains(string(out), qltyVersion) {
		t.Fatalf("Qlty --version: %s %v", out, err)
	}
	if _, err := e.BuildPlan([]string{"prewalk"}); err != nil {
		t.Fatal(err)
	}
}

func qltyTestEngine(t *testing.T) *Engine {
	t.Helper()
	b, _ := json.Marshal([]Module{{ID: "prewalk", Operations: []Operation{{Kind: "qlty-install", Root: "codex", Target: "integrations/prewalk/bin/qlty"}}}})
	home := t.TempDir()
	e, err := New(fstest.MapFS{"modules.json": &fstest.MapFile{Data: b, Mode: fs.FileMode(0644)}}, home, filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func withQltyFixture(t *testing.T, archive, binary []byte, handler http.HandlerFunc) {
	t.Helper()
	oldReleases, oldPlatform, oldBase, oldExtract := qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract
	server := httptest.NewServer(handler)
	qltyReleases = map[string]qltyRelease{"test/test": {archiveName: "fixture.tar.xz", archiveSHA256: sha256Hex(archive), binarySHA256: sha256Hex(binary)}}
	qltyPlatform = func() string { return "test/test" }
	qltyReleaseBaseURL, qltyExtract = server.URL, func([]byte, qltyRelease) ([]byte, error) { return binary, nil }
	t.Cleanup(func() {
		server.Close()
		qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract = oldReleases, oldPlatform, oldBase, oldExtract
	})
}

func TestBuildPlanNeverDownloadsQlty(t *testing.T) {
	requests := 0
	withQltyFixture(t, []byte("archive"), []byte("binary"), func(w http.ResponseWriter, _ *http.Request) { requests++; _, _ = w.Write([]byte("archive")) })
	e := qltyTestEngine(t)
	if _, err := e.BuildPlan([]string{"prewalk"}); err == nil {
		t.Fatal("BuildPlan accepted missing Qlty")
	}
	if requests != 0 {
		t.Fatalf("BuildPlan downloaded Qlty: %d requests", requests)
	}
	if err := e.installQlty(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.BuildPlan([]string{"prewalk"}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("unexpected requests after explicit install: %d", requests)
	}
}

func TestQltyInstallVerifiedAndReusesValid(t *testing.T) {
	archive, binary := []byte("archive"), []byte("qlty binary")
	requests := 0
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, _ *http.Request) { requests++; _, _ = w.Write(archive) })
	e := qltyTestEngine(t)
	p, err := e.PlanDependencies([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(p.Missing, depQlty) {
		t.Fatalf("Qlty not missing: %v", p.Missing)
	}
	if err := e.InstallDependencies(p, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.installQlty(nil); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || !e.qltyAvailable() {
		t.Fatalf("requests=%d available=%v", requests, e.qltyAvailable())
	}
}

func TestDependencyPlanIdentifiesMissingQlty(t *testing.T) {
	e := qltyTestEngine(t)
	p, err := e.PlanDependencies([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range p.Missing {
		found = found || item == depQlty
	}
	if !found || !strings.Contains(strings.Join(p.Warnings, "\n"), "Qlty") {
		t.Fatalf("missing=%v warnings=%v", p.Missing, p.Warnings)
	}
}

func TestQltyInstallRepairsUnsafeModes(t *testing.T) {
	archive, binary := []byte("archive"), []byte("qlty binary")
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	e := qltyTestEngine(t)
	if err := e.installQlty(nil); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []fs.FileMode{0644, 0777, 0755 | fs.ModeSetuid, 0755 | fs.ModeSetgid} {
		t.Run(mode.String(), func(t *testing.T) {
			if err := os.Chmod(e.managedQlty(), mode); err != nil {
				t.Fatal(err)
			}
			p, err := e.PlanDependencies([]string{"prewalk"})
			if err != nil || !contains(p.Missing, depQlty) {
				t.Fatalf("unsafe Qlty not pending: %v %v", p, err)
			}
			if e.qltyAvailable() {
				t.Fatal("unsafe Qlty accepted")
			}
			if err := e.installQlty(nil); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(e.managedQlty())
			if err != nil || writableMode(info.Mode()) != 0755 || !e.qltyAvailable() {
				t.Fatalf("Qlty permissions not repaired: %v %v", info, err)
			}
		})
	}
}

func TestQltyInstallRejectsCorruptDownloadWithoutMutation(t *testing.T) {
	archive, binary := []byte("expected"), []byte("binary")
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("corrupt")) })
	e := qltyTestEngine(t)
	target := e.managedQlty()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := e.PlanDependencies([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(p.Missing, depQlty) {
		t.Fatalf("corrupt Qlty not missing: %v", p.Missing)
	}
	if err := e.installQlty(nil); err == nil {
		t.Fatal("corrupt download accepted")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "keep" {
		t.Fatalf("target mutated: %q", got)
	}
}

func TestQltyInstallRejectsSymlink(t *testing.T) {
	e := qltyTestEngine(t)
	target := e.managedQlty()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), target); err != nil {
		t.Fatal(err)
	}
	if err := e.installQlty(nil); err == nil {
		t.Fatal("symlink accepted")
	}
}
