package installer

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const zgTarballURL = "https://registry.npmjs.org/@zvec/zvec-grep/-/zvec-grep-0.2.1.tgz"
const zgTarballHash = "trVTNazVGbF5IDr6r7tKzfsuXArhxLRUCD1R3lVdPt9VppwldJ5OjUkjVBqXUMO2/3zZ5Wefl0+34X0TWG8KkQ=="

func (e *Engine) installZG(stdout, stderr io.Writer) error {
	if err := e.installZGNode(stdout); err != nil {
		return err
	}
	destination := e.managedZGPackages()
	if err := checkPath(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return e.validateZGPackage(destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".packages-stage-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	archive, err := downloadZGPackage()
	if err != nil {
		return err
	}
	archivePath := filepath.Join(stage, "zg.tgz")
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		return err
	}
	if err := e.npmInstallZG(stage, archivePath, stdout, stderr); err != nil {
		return err
	}
	if err := e.zgPatch("apply", zgPackage(stage)); err != nil {
		return err
	}
	if err := e.validateZGPackage(stage); err != nil {
		return err
	}
	if err := os.Remove(archivePath); err != nil {
		return err
	}
	return os.Rename(stage, destination)
}

func downloadZGPackage() ([]byte, error) {
	resp, err := zgNodeClient.Get(zgTarballURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zg: descarga HTTP %d", resp.StatusCode)
	}
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	digest := sha512.Sum512(data)
	if len(data) > limit || base64.StdEncoding.EncodeToString(digest[:]) != zgTarballHash {
		return nil, fmt.Errorf("zg: integridad del paquete incorrecta")
	}
	return data, nil
}

func (e *Engine) npmInstallZG(stage, archive string, stdout, stderr io.Writer) error {
	// Empty private configs and stripped npm overrides avoid global installs,
	// user lifecycle settings, credential forwarding and a user-selected registry.
	for _, name := range []string{"npmrc", "global-npmrc"} {
		if err := os.WriteFile(filepath.Join(stage, name), nil, 0600); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.managedZGNode(), e.managedZGNPM(), "install", "--ignore-scripts", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org", "--prefix="+stage, "--cache="+filepath.Join(stage, "npm-cache"), "--userconfig="+filepath.Join(stage, "npmrc"), "--globalconfig="+filepath.Join(stage, "global-npmrc"), archive)
	cmd.Dir, cmd.Stdout, cmd.Stderr = stage, stdout, stderr
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToLower(key)
		if strings.HasPrefix(key, "npm_config_") || key == "node_options" || key == "node_path" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zg: npm privado: %w", err)
	}
	return nil
}
