package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryIntegrityDetectsContentNamesAndLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("known"), 0600); err != nil {
		t.Fatal(err)
	}
	want, err := directorySHA256(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if got, _ := directorySHA256(dir); got != want {
		t.Fatal("umask affected integrity")
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, _ := directorySHA256(dir); got == want {
		t.Fatal("modified content accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", path); err != nil {
		t.Fatal(err)
	}
	if got, _ := directorySHA256(dir); got == want {
		t.Fatal("modified link accepted")
	}
}

// Maintainer evidence: derive pins from checksum-verified archives and an npm-ci
// tree generated from the packaged lock, never from a user's live installation.
func TestZGPinArtifacts(t *testing.T) {
	root := os.Getenv("CODEX_SETUP_PIN_ARTIFACTS")
	if root == "" {
		t.Skip("set CODEX_SETUP_PIN_ARTIFACTS to an isolated artifact directory")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) { checkZGPinArtifacts(t, root, arch) })
	}
}

func checkZGPinArtifacts(t *testing.T, root, arch string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "node-"+arch+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(data) != zgNodeHashes[arch] {
		t.Fatal("unverified Node archive")
	}
	stage := t.TempDir()
	if err := extractZGNode(data, stage, strings.TrimSuffix(zgNodeAssets[arch], ".tar.gz")); err != nil {
		t.Fatal(err)
	}
	digest, err := directorySHA256(stage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Node %s: %s", arch, digest)
	if digest != zgNodeRuntimeHashes[arch] {
		t.Fatal("Node runtime pin mismatch")
	}
	digest, err = directorySHA256(filepath.Join(root, arch, "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("npm %s: %s", arch, digest)
	if digest != zgPackageRuntimeHashes[arch] {
		t.Fatal("npm runtime pin mismatch")
	}
}
