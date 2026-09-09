package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type rtkRoundTripper func(*http.Request) (*http.Response, error)

func (f rtkRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadRTKRejectsBadChecksum(t *testing.T) {
	old := rtkHTTPClient
	rtkHTTPClient = &http.Client{Transport: rtkRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not-the-release")), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { rtkHTTPClient = old })
	if _, err := downloadRTK(); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestInstallRTKPreservesValidManagedBinaryWithoutDownload(t *testing.T) {
	root := t.TempDir()
	e := &Engine{CodexHome: filepath.Join(root, ".codex"), Home: root}
	target := e.managedRTK()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("#!/bin/sh\necho 'rtk 0.40.0'\n")
	if err := os.WriteFile(target, content, 0755); err != nil {
		t.Fatal(err)
	}
	old := rtkHTTPClient
	rtkHTTPClient = &http.Client{Transport: rtkRoundTripper(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected download"); return nil, nil })}
	t.Cleanup(func() { rtkHTTPClient = old })
	if err := e.installRTK(io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("managed binary changed: %q", got)
	}
}

func TestInstallRTKPreservesOriginalWhenStagedValidationFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	e := &Engine{CodexHome: filepath.Join(root, ".codex"), Home: root}
	target := e.managedRTK()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("original")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatal(err)
	}
	previous := target + ".previous"
	if err := os.WriteFile(previous, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	bad := []byte("#!/bin/sh\necho invalid\n")
	if err := tw.WriteHeader(&tar.Header{Name: "rtk", Mode: 0755, Size: int64(len(bad))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(bad)
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	oldHash := rtkHashes[runtime.GOARCH]
	rtkHashes[runtime.GOARCH] = hex.EncodeToString(sum[:])
	t.Cleanup(func() { rtkHashes[runtime.GOARCH] = oldHash })
	oldClient := rtkHTTPClient
	rtkHTTPClient = &http.Client{Transport: rtkRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive.Bytes())), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { rtkHTTPClient = oldClient })
	if err := e.installRTK(io.Discard); err == nil {
		t.Fatal("invalid staged binary accepted")
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, original) {
		t.Fatalf("original changed: %q", got)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode changed: %o", info.Mode().Perm())
	}
	owned, _ := os.ReadFile(previous)
	if string(owned) != "owned" {
		t.Fatal("unrelated previous file changed")
	}
}
