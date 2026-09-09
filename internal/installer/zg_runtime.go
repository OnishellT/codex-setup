package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
const zgNodeMaxDownload = 256 << 20

var zgNodeClient = &http.Client{Timeout: 2 * time.Minute}
var zgNodeAssets = map[string]string{"amd64": "node-v22.23.2-linux-x64.tar.gz", "arm64": "node-v22.23.2-linux-arm64.tar.gz"}
var zgNodeHashes = map[string]string{"amd64": "b294a556e639d64338823920e5866c21c02741742d2e1529ee1a225c1ec9252a", "arm64": "013b59cfd2819703a6f4a14ab891fc46fc2a4e3f5bcd92de3fb4929b43e35b30"}

func (e *Engine) managedZGNode() string {
	return filepath.Join(e.CodexHome, "integrations", "zg", "node-v"+zgNodeVersion, "bin", "node")
}
func (e *Engine) managedZGNPM() string {
	return filepath.Join(filepath.Dir(e.managedZGNode()), "npm", "npm-cli.js")
}

func (e *Engine) installZGNode(stdout io.Writer) error {
	if runtime.GOOS != "linux" {
		return errors.New("Node gestionado de zg solo está disponible en Linux")
	}
	asset, ok := zgNodeAssets[runtime.GOARCH]
	if !ok {
		return fmt.Errorf("Node gestionado no soportado en %s", runtime.GOARCH)
	}
	resp, err := zgNodeClient.Get("https://nodejs.org/dist/v" + zgNodeVersion + "/" + asset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("descarga Node devolvió HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, zgNodeMaxDownload+1))
	if err != nil {
		return err
	}
	if len(data) > zgNodeMaxDownload {
		return errors.New("archivo Node excede tamaño permitido")
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != zgNodeHashes[runtime.GOARCH] {
		return errors.New("checksum Node no coincide")
	}
	node, npm, err := zgNodeFiles(data)
	if err != nil {
		return err
	}
	base := filepath.Dir(filepath.Dir(e.managedZGNode()))
	if err := checkPath(base); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.managedZGNode()), 0700); err != nil {
		return err
	}
	for _, path := range []string{e.managedZGNode(), e.managedZGNPM()} {
		if info, statErr := os.Lstat(path); statErr == nil && !info.Mode().IsRegular() {
			return errors.New("destino Node existente inválido")
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.managedZGNode()), ".node-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0755); err == nil {
		_, err = tmp.Write(node)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.WriteFile(name+".npm", npm, 0700); err != nil {
		return err
	}
	defer os.Remove(name + ".npm")
	if !validExactNode(name) {
		return errors.New("binario Node descargado no supera la verificación")
	}
	if _, err = os.Stat(e.managedZGNode()); err == nil {
		if err = os.Remove(e.managedZGNode()); err != nil {
			return err
		}
	}
	if err = os.Rename(name, e.managedZGNode()); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(e.managedZGNPM()), 0700); err != nil {
		return err
	}
	if err = os.Rename(name+".npm", e.managedZGNPM()); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, "Node gestionado instalado en "+e.managedZGNode()+"\n")
	}
	return nil
}

func validExactNode(path string) bool {
	out, err := run(5*time.Second, path, "--version")
	return err == nil && strings.TrimSpace(out) == "v"+zgNodeVersion
}

func validVersionBinary(path, major string) bool {
	out, err := run(5*time.Second, path, "--version")
	return err == nil && strings.HasPrefix(strings.TrimSpace(out), "v"+major+".")
}

func zgNodeFiles(data []byte) ([]byte, []byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var node, npm []byte
	for {
		h, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, nil, nextErr
		}
		if h.Typeflag != tar.TypeReg || strings.Contains(h.Name, "..") {
			continue
		}
		base := filepath.ToSlash(h.Name)
		if strings.HasSuffix(base, "/bin/node") {
			if h.Size > 128<<20 {
				return nil, nil, errors.New("binario Node excede tamaño permitido")
			}
			node, err = io.ReadAll(io.LimitReader(tr, 128<<20))
			if err != nil {
				return nil, nil, err
			}
		} else if strings.HasSuffix(base, "/lib/node_modules/npm/bin/npm-cli.js") {
			npm, err = io.ReadAll(io.LimitReader(tr, 8<<20))
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if len(node) == 0 || len(npm) == 0 {
		return nil, nil, errors.New("archivo Node no contiene bin/node y npm")
	}
	return node, npm, nil
}
