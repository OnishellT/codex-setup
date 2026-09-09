package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const zgArchiveSuffix = ".tar.gz"

type nodeArchiveEntry struct {
	name, body string
	kind       byte
}

func nodeArchive(t *testing.T, entries []nodeArchiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, f := range entries {
		kind := f.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: kind, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func validNodeFixture(t *testing.T) []byte {
	root := strings.TrimSuffix(zgNodeAssets[runtime.GOARCH], zgArchiveSuffix)
	node := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v22.23.2; exit; fi\n[ -f \"${1%/bin/npm-cli.js}/lib/required.js\" ] || exit 1\necho 10.9.8\n"
	return nodeArchive(t, []nodeArchiveEntry{{root + "/bin/node", node, 0}, {root + "/" + zgNPMRelative, "npm", 0}, {root + "/lib/node_modules/npm/lib/required.js", "required", 0}})
}

func mockNodeDownload(t *testing.T, data []byte, pin bool) {
	t.Helper()
	oldClient, oldHash := zgNodeClient, zgNodeHashes[runtime.GOARCH]
	t.Cleanup(func() { zgNodeClient = oldClient; zgNodeHashes[runtime.GOARCH] = oldHash })
	zgNodeClient = &http.Client{Transport: rtkRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	if pin {
		zgNodeHashes[runtime.GOARCH] = sha256Hex(data)
		stage := t.TempDir()
		if err := extractZGNode(data, stage, strings.TrimSuffix(zgNodeAssets[runtime.GOARCH], zgArchiveSuffix)); err != nil {
			t.Fatal(err)
		}
		oldRuntime := zgNodeRuntimeHashes[runtime.GOARCH]
		digest, err := directorySHA256(stage)
		if err != nil {
			t.Fatal(err)
		}
		zgNodeRuntimeHashes[runtime.GOARCH] = digest
		t.Cleanup(func() { zgNodeRuntimeHashes[runtime.GOARCH] = oldRuntime })
	}
}

func TestInstallZGNodeCompleteRuntimeAndReuse(t *testing.T) {
	mockNodeDownload(t, validNodeFixture(t), true)
	e := &Engine{CodexHome: t.TempDir()}
	if err := e.installZGNode(io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := validateZGNode(filepath.Dir(filepath.Dir(e.managedZGNode()))); err != nil {
		t.Fatal(err)
	}
	zgNodeClient = &http.Client{Transport: rtkRoundTripper(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected download"); return nil, nil })}
	if err := e.installZGNode(io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestInstallZGNodeRejectsChecksumAndPreservesExisting(t *testing.T) {
	mockNodeDownload(t, validNodeFixture(t), false)
	e := &Engine{CodexHome: t.TempDir()}
	if err := e.installZGNode(io.Discard); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, err := os.Stat(e.managedZGNode()); !os.IsNotExist(err) {
		t.Fatal("created runtime despite invalid checksum")
	}
	dir := filepath.Dir(e.managedZGNode())
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.managedZGNode(), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.installZGNode(io.Discard); err == nil {
		t.Fatal("invalid existing runtime accepted")
	}
	data, err := os.ReadFile(e.managedZGNode())
	if err != nil || string(data) != "original" {
		t.Fatal("existing runtime changed")
	}
}

func TestZGNodeArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, entry := range []nodeArchiveEntry{
		{"root/../escape", "x", 0}, {"/absolute", "x", 0}, {"other/bin/node", "x", 0},
		{"root/bin/node", "", tar.TypeSymlink}, {"root/lib/node_modules/npm/link", "", tar.TypeLink},
	} {
		t.Run(entry.name, func(t *testing.T) {
			if err := extractZGNode(nodeArchive(t, []nodeArchiveEntry{entry}), t.TempDir(), "root"); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}

func TestInstallZGNodeFailedValidationLeavesNoRuntime(t *testing.T) {
	root := strings.TrimSuffix(zgNodeAssets[runtime.GOARCH], zgArchiveSuffix)
	mockNodeDownload(t, nodeArchive(t, []nodeArchiveEntry{{root + "/bin/node", "#!/bin/sh\necho v22.23.2\n", 0}}), true)
	e := &Engine{CodexHome: t.TempDir()}
	if err := e.installZGNode(io.Discard); err == nil {
		t.Fatal("missing npm accepted")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(e.managedZGNode()))); !os.IsNotExist(err) {
		t.Fatal("incomplete runtime exposed")
	}
}

// Opt-in real archive check: no network, indexes, models, or live CODEX_HOME.
func TestZGNodeOfficialArchive(t *testing.T) {
	path := os.Getenv("CODEX_SETUP_NODE_ARCHIVE")
	if path == "" {
		t.Skip("set CODEX_SETUP_NODE_ARCHIVE to a downloaded official archive")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mockNodeDownload(t, data, false)
	e := &Engine{CodexHome: t.TempDir()}
	if err := e.installZGNode(io.Discard); err != nil {
		t.Fatal(err)
	}
}
