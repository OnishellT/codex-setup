package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const zgNodeVersion = "22.23.2"
const zgNodeMaxDownload = 80 << 20
const zgNPMRelative = "lib/node_modules/npm/bin/npm-cli.js"

var zgNodeClient = &http.Client{Timeout: 2 * time.Minute}
var zgNodeAssets = map[string]string{"amd64": "node-v22.23.2-linux-x64.tar.gz", "arm64": "node-v22.23.2-linux-arm64.tar.gz"}
var zgNodeHashes = map[string]string{"amd64": "b294a556e639d64338823920e5866c21c02741742d2e1529ee1a225c1ec9252a", "arm64": "013b59cfd2819703a6f4a14ab891fc46fc2a4e3f5bcd92de3fb4929b43e35b30"}

func (e *Engine) managedZGNode() string {
	return filepath.Join(e.CodexHome, "integrations", "zg", "node-v"+zgNodeVersion, "bin", "node")
}
func (e *Engine) managedZGNPM() string {
	return filepath.Join(filepath.Dir(filepath.Dir(e.managedZGNode())), zgNPMRelative)
}

func (e *Engine) installZGNode(stdout io.Writer) error {
	asset, ok := zgNodeAssets[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return errors.New("Node gestionado requiere Linux amd64/arm64")
	}
	destination := filepath.Dir(filepath.Dir(e.managedZGNode()))
	if err := checkPath(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := validateZGNode(destination); err != nil {
			return fmt.Errorf("runtime Node existente inválido; se conserva %s: %w", destination, err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := downloadZGNode(asset)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".node-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extractZGNode(data, stage, strings.TrimSuffix(asset, ".tar.gz")); err != nil {
		return err
	}
	if err := validateZGNode(stage); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("destino Node cambió durante la instalación; no se reemplazó")
	}
	if err := os.Rename(stage, destination); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, "Node y npm gestionados instalados en "+destination+"\n")
	}
	return nil
}

func downloadZGNode(asset string) ([]byte, error) {
	resp, err := zgNodeClient.Get("https://nodejs.org/dist/v" + zgNodeVersion + "/" + asset)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("descarga Node devolvió HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, zgNodeMaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > zgNodeMaxDownload {
		return nil, errors.New("archivo Node excede tamaño permitido")
	}
	if sha256Hex(data) != zgNodeHashes[runtime.GOARCH] {
		return nil, errors.New("checksum Node no coincide")
	}
	return data, nil
}

func validateZGNode(root string) error {
	node := filepath.Join(root, "bin", "node")
	npm := filepath.Join(root, zgNPMRelative)
	for _, path := range []string{node, npm} {
		if err := checkPath(path); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("runtime Node incompleto: %s", path)
		}
	}
	out, err := run(5*time.Second, node, "--version")
	if err != nil || strings.TrimSpace(out) != "v"+zgNodeVersion {
		return errors.New("versión Node gestionada no coincide")
	}
	out, err = run(5*time.Second, node, npm, "--version")
	if err != nil || strings.TrimSpace(out) == "" {
		return errors.New("npm gestionado no funciona")
	}
	return nil
}

func extractZGNode(data []byte, stage, archiveRoot string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for count := 0; count < 20000; count++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		total += h.Size
		if h.Size < 0 || total > 256<<20 {
			return errors.New("archivo Node excede límite de extracción")
		}
		if err := extractZGNodeEntry(tr, h, stage, archiveRoot); err != nil {
			return err
		}
	}
	return errors.New("archivo Node contiene demasiadas entradas")
}

func extractZGNodeEntry(tr io.Reader, h *tar.Header, stage, archiveRoot string) error {
	name := strings.TrimSuffix(h.Name, "/")
	if name == archiveRoot && h.Typeflag == tar.TypeDir {
		return nil
	}
	relative, ok := strings.CutPrefix(name, archiveRoot+"/")
	if !ok || !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != relative {
		return errors.New("ruta inválida en archivo Node")
	}
	selected := relative == "bin/node" || strings.HasPrefix(relative, "lib/node_modules/npm/")
	if !selected || h.Typeflag == tar.TypeDir {
		return nil
	}
	if h.Typeflag != tar.TypeReg || h.Size > 128<<20 {
		return errors.New("entrada Node/npm no regular o demasiado grande")
	}
	target := filepath.Join(stage, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if relative == "bin/node" {
		mode = 0755
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, tr, h.Size)
	return errors.Join(copyErr, file.Close())
}
