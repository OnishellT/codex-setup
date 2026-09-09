package installer

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func qltyTestEngine(t *testing.T) *Engine {
	t.Helper()
	modules, err := json.Marshal([]Module{{ID: "prewalk", Operations: []Operation{{Kind: "qlty-install", Root: "codex", Target: "integrations/prewalk/bin/qlty"}}}})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	engine, err := New(fstest.MapFS{"modules.json": &fstest.MapFile{Data: modules, Mode: fs.FileMode(0644)}}, home, filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func withQltyFixture(t *testing.T, archive, binary []byte, handler http.HandlerFunc) {
	t.Helper()
	oldReleases, oldPlatform, oldBase, oldExtract := qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract
	server := httptest.NewServer(handler)
	qltyReleases = map[string]qltyRelease{"test/test": {
		archiveName: "fixture.tar.xz", archiveSHA256: sha256Hex(archive), binarySHA256: sha256Hex(binary),
	}}
	qltyPlatform = func() string { return "test/test" }
	qltyReleaseBaseURL = server.URL
	qltyExtract = func(got []byte, release qltyRelease) ([]byte, error) { return binary, nil }
	t.Cleanup(func() {
		server.Close()
		qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract = oldReleases, oldPlatform, oldBase, oldExtract
	})
}

func TestQltyInstallVerifiedAndIdempotent(t *testing.T) {
	archive, binary := []byte("archive"), []byte("qlty binary")
	requests := 0
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/fixture.tar.xz" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write(archive)
	})
	engine := qltyTestEngine(t)
	plan, err := engine.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(plan.Changes))
	}
	if _, err = engine.Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(engine.CodexHome, "integrations", "prewalk", "bin", "qlty")
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(binary) {
		t.Fatalf("installed binary = %q, %v", got, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v, %v; want 0755", info.Mode(), err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	plan, err = engine.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 || requests != 1 {
		t.Fatalf("second plan changes=%d requests=%d, want 0 and 1", len(plan.Changes), requests)
	}
	if err = os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	plan, err = engine.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || requests != 1 {
		t.Fatalf("mode repair changes=%d requests=%d, want 1 and 1", len(plan.Changes), requests)
	}
	if _, err = engine.Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("repaired mode = %v, %v; want 0755", info.Mode(), err)
	}
	if err = os.Chmod(target, 0755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	plan, err = engine.BuildPlan([]string{"prewalk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || requests != 1 {
		t.Fatalf("special mode repair changes=%d requests=%d, want 1 and 1", len(plan.Changes), requests)
	}
	if _, err = engine.Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		t.Fatalf("repaired special mode = %v, %v; want plain 0755", info.Mode(), err)
	}
}

func TestQltyInstallRejectsCorruptDownloadWithoutMutation(t *testing.T) {
	archive, binary := []byte("expected archive"), []byte("qlty binary")
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("corrupt")) })
	engine := qltyTestEngine(t)
	target := filepath.Join(engine.CodexHome, "integrations", "prewalk", "bin", "qlty")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.BuildPlan([]string{"prewalk"}); err == nil {
		t.Fatal("BuildPlan accepted a corrupt download")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "keep" {
		t.Fatalf("target mutated: %q, %v", got, err)
	}
}

func TestQltyModeSanitizingRollsBackExactly(t *testing.T) {
	archive, binary := []byte("archive"), []byte("qlty binary")
	withQltyFixture(t, archive, binary, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) })
	engine := qltyTestEngine(t)
	target := filepath.Join(engine.CodexHome, "integrations", "prewalk", "bin", "qlty")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, binary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	plan, err := engine.BuildPlan([]string{"prewalk"})
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("mode repair plan = %v, %v", plan, err)
	}
	failPath := filepath.Join(engine.CodexHome, "zz-fail", "file")
	plan.Changes = append(plan.Changes, Change{Path: failPath, Kind: "crear", data: []byte("new"), mode: 0600})
	failed := false
	_, err = engine.Apply(plan, func(message string) {
		if !failed && message == "crear "+failPath {
			failed = true
			if linkErr := os.Symlink(t.TempDir(), filepath.Dir(failPath)); linkErr != nil {
				t.Fatalf("create failure trigger: %v", linkErr)
			}
		}
	})
	if err == nil {
		t.Fatal("Apply succeeded despite the injected path conflict")
	}
	got, readErr := os.ReadFile(target)
	info, statErr := os.Stat(target)
	if readErr != nil || statErr != nil || string(got) != string(binary) || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != os.ModeSetuid {
		t.Fatalf("rollback did not restore Qlty exactly: content=%q mode=%v read=%v stat=%v", got, info.Mode(), readErr, statErr)
	}
}

func TestQltyInstallRejectsUnsupportedPlatform(t *testing.T) {
	oldPlatform := qltyPlatform
	qltyPlatform = func() string { return "plan9/amd64" }
	t.Cleanup(func() { qltyPlatform = oldPlatform })
	if _, err := qltyTestEngine(t).BuildPlan([]string{"prewalk"}); err == nil {
		t.Fatal("BuildPlan accepted unsupported platform")
	}
}

func TestQltyInstallRejectsInvalidArchive(t *testing.T) {
	archive, binary := []byte("not an xz archive"), []byte("qlty binary")
	oldReleases, oldPlatform, oldBase, oldExtract := qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	qltyReleases = map[string]qltyRelease{"test/test": {archiveName: "fixture.tar.xz", archiveSHA256: sha256Hex(archive), binarySHA256: sha256Hex(binary)}}
	qltyPlatform, qltyReleaseBaseURL, qltyExtract = func() string { return "test/test" }, server.URL, extractQlty
	t.Cleanup(func() {
		server.Close()
		qltyReleases, qltyPlatform, qltyReleaseBaseURL, qltyExtract = oldReleases, oldPlatform, oldBase, oldExtract
	})
	engine := qltyTestEngine(t)
	if _, err := engine.BuildPlan([]string{"prewalk"}); err == nil {
		t.Fatal("BuildPlan accepted invalid archive")
	}
	target := filepath.Join(engine.CodexHome, "integrations", "prewalk", "bin", "qlty")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid archive created target: %v", err)
	}
}
