package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRTKWarningRequiresManagedBinary(t *testing.T) {
	root := t.TempDir()
	e := &Engine{CodexHome: filepath.Join(root, ".codex")}
	path := filepath.Join(root, "rtk")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'rtk 0.40.0'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	warnings := strings.Join(e.hookPrerequisiteWarnings("rtk"), "\n")
	if !strings.Contains(warnings, e.managedRTK()) {
		t.Fatalf("PATH binary incorrectly satisfied managed requirement: %s", warnings)
	}
	if err := os.MkdirAll(filepath.Dir(e.managedRTK()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, e.managedRTK()); err != nil {
		t.Fatal(err)
	}
	if warnings = strings.Join(e.hookPrerequisiteWarnings("rtk"), "\n"); strings.Contains(warnings, "rtk:") {
		t.Fatalf("valid managed binary still warned: %s", warnings)
	}
}
