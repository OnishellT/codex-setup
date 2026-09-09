package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestZGNodeFilesExtractsRequiredRegularFiles(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	files := []struct{ name, data string }{{"node-v22/bin/node", "node"}, {"node-v22/lib/node_modules/npm/npm-cli.js", "npm"}}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(f.data))
	}
	_ = tw.Close()
	_ = gz.Close()
	node, npm, err := zgNodeFiles(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(node) != "node" || string(npm) != "npm" {
		t.Fatalf("files = %q/%q", node, npm)
	}
}

func TestZGNodeFilesRejectsMissingRequiredFiles(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "node-v22/bin/node", Typeflag: tar.TypeReg, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	if _, _, err := zgNodeFiles(b.Bytes()); err == nil {
		t.Fatal("missing npm accepted")
	}
}
