package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestZGPreflight(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux patch guard")
	}
	bin := t.TempDir()
	realNode, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for eval smoke test")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "@zvec", "zvec-grep")
	if err := os.MkdirAll(filepath.Join(pkg, "dist", "cli"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@zvec/zvec-grep","version":"0.2.1","bin":{"zg":"dist/cli/index.js"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(pkg, "dist", "cli", "index.js")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cli, filepath.Join(bin, "zg")); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("npm", "#!/bin/sh\necho '"+root+"'\n")
	write("node", "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v24.0.0; else echo \"${ZG_CHECK:-patched}\"; fi\n")
	t.Setenv("PATH", bin)
	assets := fstest.MapFS{
		"modules.json":   &fstest.MapFile{Data: []byte(`[{"id":"zg","operations":[{"kind":"merge","source":"config/zg.toml","root":"codex","target":"config.toml"}]}]`)},
		"config/zg.toml": &fstest.MapFile{Data: []byte("[mcp_servers.zvec_grep]\nenabled = true\n")},
		zgHelper:         &fstest.MapFile{Data: []byte("// fixture helper")},
	}
	e, err := New(assets, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	smoke := `export async function run(command, root) { if (command !== "check" || process.argv[1] !== root) throw new Error("argv"); return "patched"; }`
	out, err := run(3*time.Second, realNode, "--input-type=module", "--eval", smoke+"\nconsole.log(await run(\"check\", process.argv[1]));", pkg)
	if err != nil || strings.TrimSpace(out) != "patched" {
		t.Fatalf("real node eval smoke: %q, %v", out, err)
	}
	if _, err = e.BuildPlan([]string{"zg"}); err != nil {
		t.Fatalf("patched preflight: %v", err)
	}
	t.Setenv("ZG_CHECK", "original")
	if _, err = e.BuildPlan([]string{"zg"}); err == nil {
		t.Fatal("unpatched zg passed")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err = e.BuildPlan([]string{"zg"}); err == nil {
		t.Fatal("missing node passed")
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@zvec/zvec-grep","version":"0.2.2","bin":{"zg":"dist/cli/index.js"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ZG_CHECK", "patched")
	if _, err = e.BuildPlan([]string{"zg"}); err == nil {
		t.Fatal("unknown package passed")
	}
}
