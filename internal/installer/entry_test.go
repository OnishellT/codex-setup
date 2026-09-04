package installer

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellHookIdempotenceAndMalformedBlocks(t *testing.T) {
	original := "# user's existing shell\nexport EDITOR=micro\n"
	snippet := "# >>> codex-setup:launcher >>>\n: test\n# <<< codex-setup:launcher <<<"
	first, err := shellBlock([]byte(original), snippet)
	if err != nil {
		t.Fatal(err)
	}
	second, err := shellBlock(first, snippet)
	if err != nil || !bytes.Equal(first, second) || !strings.HasPrefix(string(first), original) {
		t.Fatal("not a preserving/idempotent hook")
	}
	changed, err := shellBlock(first, strings.Replace(snippet, ": test", ": updated", 1))
	if err != nil || strings.Count(string(changed), "# >>>") != 1 || !bytes.Contains(changed, []byte(": updated")) {
		t.Fatal("managed update failed")
	}
	for _, malformed := range []string{"# >>> codex-setup:launcher >>>", "# <<< codex-setup:launcher <<<", snippet + snippet} {
		if _, err = shellBlock([]byte(malformed), snippet); err == nil {
			t.Fatal("accepted malformed markers")
		}
	}
}

func TestShellHookNixFallbackAndRealExecutableUntouched(t *testing.T) {
	home := t.TempDir()
	e := &Engine{Home: home, CodexHome: filepath.Join(home, ".codex")}
	original := filepath.Join(home, "managed-zshrc")
	if err := os.WriteFile(original, []byte("# Nix-managed\n"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	if got := e.zshHookPath(); got != filepath.Join(home, ".zshenv") {
		t.Fatal(got)
	}
	files := map[string][]byte{}
	get := func(path string) (*Change, error) { return &Change{Path: path, data: files[path]}, nil }
	put := func(path string, data []byte, mode fs.FileMode) error { files[path] = data; return nil }
	plan := &Plan{}
	if err := e.prepareEntry(plan, "/usr/bin/python3", "/package manager/codex", "/panel's launcher", put, get); err != nil {
		t.Fatal(err)
	}
	if _, ok := files[filepath.Join(home, ".zshrc")]; ok {
		t.Fatal("rewrites managed zshrc")
	}
	if _, ok := files[filepath.Join(home, ".local", "bin", "codex")]; ok {
		t.Fatal("replaces package-manager executable")
	}
	if len(files) != 3 {
		t.Fatalf("want wrapper + 2 shell hooks, got %d", len(files))
	}
	for _, name := range []string{".bashrc", ".zshenv"} {
		if !bytes.Contains(files[filepath.Join(home, name)], []byte("case $- in")) {
			t.Fatal("unguarded shell hook")
		}
	}
}

func TestGeneratedShellFunctionPreservesArgumentsAndOnlyInteractive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user's home")
	e := &Engine{Home: home, CodexHome: filepath.Join(home, ".codex")}
	files := map[string][]byte{}
	get := func(path string) (*Change, error) { return &Change{Path: path}, nil }
	put := func(path string, b []byte, _ fs.FileMode) error { files[path] = b; return nil }
	if err := e.prepareEntry(&Plan{}, "/python", "/official/codex", "/panel", put, get); err != nil {
		t.Fatal(err)
	}
	// Substitute a harmless argument-printer for the generated dispatch wrapper.
	wrapper := filepath.Join(home, ".local", "bin", "codex-with-panel")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " unavailable")
			}
			rc := filepath.Join(home, ".bashrc")
			if shell == "zsh" {
				rc = filepath.Join(home, ".zshrc")
			}
			if err = os.WriteFile(rc, files[rc], 0600); err != nil {
				t.Fatal(err)
			}
			flags := []string{"--noprofile", "--norc"}
			if shell == "zsh" {
				flags = []string{"-d", "-f"}
			}
			args := append(append([]string{}, flags...), "-ic", "source \"$1\"; codex --yolo -p work \"a b\" \"\"", "test", rc)
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%v %s", err, out)
			}
			if !bytes.Contains(out, []byte("<--yolo>\n<-p>\n<work>\n<a b>\n<>\n")) {
				t.Fatalf("argv changed: %s", out)
			}
			check := "source \"$1\"; declare -F codex"
			if shell == "zsh" {
				check = "source \"$1\"; (( $+functions[codex] ))"
			}
			args = append(append([]string{}, flags...), "-c", check, "test", rc)
			if err = exec.Command(binary, args...).Run(); err == nil {
				t.Fatal("function defined in noninteractive shell")
			}
		})
	}
}

func TestAtLeastVersion(t *testing.T) {
	for _, tc := range []struct {
		output     string
		major, min int
		want       bool
	}{
		{"rtk 0.23.0", 0, 23, true},
		{"v18.20.4", 18, 0, true},
		{"v17.99.0", 18, 0, false},
		{"rtk 0.22.99", 0, 23, false},
		{"unknown", 0, 23, false},
	} {
		if got := atLeastVersion(tc.output, tc.major, tc.min); got != tc.want {
			t.Errorf("atLeastVersion(%q, %d, %d) = %v, want %v", tc.output, tc.major, tc.min, got, tc.want)
		}
	}
}
