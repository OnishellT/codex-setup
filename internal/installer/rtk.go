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
	"time"
)

const rtkVersion = "0.40.0"
const rtkMaxDownload = 32 << 20

var rtkHTTPClient = &http.Client{Timeout: 2 * time.Minute}

var rtkAssets = map[string]string{
	"amd64": "rtk-x86_64-unknown-linux-musl.tar.gz",
	"arm64": "rtk-aarch64-unknown-linux-gnu.tar.gz",
}
var rtkHashes = map[string]string{
	"amd64": "a75d210a445874106bc16da2b4efba01d36d297afa33ec134728f2d5f42ef5af",
	"arm64": "1d0087ad62a182c0833c2251ac678b5e05356418d91aa57305ac51a126c9b102",
}

func (e *Engine) managedRTK() string {
	return filepath.Join(e.CodexHome, "integrations", "rtk", "bin", "rtk")
}

func validRTK(path string) bool {
	out, err := run(3*time.Second, path, "--version")
	return err == nil && atLeastVersion(out, 0, 23)
}

func (e *Engine) rtkAvailable() bool {
	if info, err := os.Lstat(e.managedRTK()); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		return validRTK(e.managedRTK())
	}
	return false
}

func (e *Engine) installRTK(stdout io.Writer) error {
	if e.rtkAvailable() {
		return nil
	}
	archive, err := downloadRTK()
	if err != nil {
		return err
	}
	binary, err := rtkBinary(archive)
	if err != nil {
		return err
	}
	dir := filepath.Dir(e.managedRTK())
	if err := checkPath(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(e.managedRTK()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("destino RTK con enlace simbólico")
	}
	tmpName, err := stageRTKBinary(dir, binary)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if !validRTK(tmpName) {
		return errors.New("binario RTK descargado no supera la verificación de versión")
	}
	change := Change{Path: e.managedRTK(), data: binary, mode: 0755, originalMode: 0755}
	if old, readErr := os.ReadFile(change.Path); readErr == nil {
		change.existed, change.original = true, old
		if info, statErr := os.Stat(change.Path); statErr == nil {
			change.originalMode, change.mode = writableMode(info.Mode()), 0755
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if _, err = e.Apply(&Plan{owner: e, Changes: []Change{change}}, nil); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, "RTK gestionado instalado en "+e.managedRTK()+"\n")
	}
	return nil
}

func stageRTKBinary(dir string, binary []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, ".rtk-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(0755); err == nil {
		_, err = tmp.Write(binary)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	cleanup = false
	return name, nil
}

func downloadRTK() ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("RTK gestionado solo está disponible en Linux")
	}
	asset, ok := rtkAssets[runtime.GOARCH]
	if !ok {
		return nil, fmt.Errorf("RTK no soportado en %s", runtime.GOARCH)
	}
	req, err := http.NewRequest(http.MethodGet, "https://github.com/rtk-ai/rtk/releases/download/v"+rtkVersion+"/"+asset, nil)
	if err != nil {
		return nil, err
	}
	resp, err := rtkHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("descarga RTK falló: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("descarga RTK devolvió HTTP %s", resp.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, rtkMaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(archive) > rtkMaxDownload {
		return nil, errors.New("archivo RTK excede el tamaño permitido")
	}
	hash := sha256.Sum256(archive)
	if hex.EncodeToString(hash[:]) != rtkHashes[runtime.GOARCH] {
		return nil, errors.New("checksum RTK no coincide")
	}
	return archive, nil
}

func rtkBinary(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil {
		return nil, errors.New("archivo RTK no contiene binario rtk")
	}
	if h.Typeflag != tar.TypeReg || h.Name != "rtk" || h.Size <= 0 || h.Size > 16<<20 {
		return nil, errors.New("binario RTK inválido")
	}
	return io.ReadAll(io.LimitReader(tr, h.Size))
}
