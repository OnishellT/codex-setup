package installer

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := fs.ReadFile(e.assets, "integrations/zg/"+name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
			return err
		}
	}
	if err := e.npmInstallZG(stage, stdout, stderr); err != nil {
		return err
	}
	if err := e.zgPatch("apply", zgPackage(stage)); err != nil {
		return err
	}
	if err := e.validateZGPackage(stage); err != nil {
		return err
	}
	return os.Rename(stage, destination)
}

func (e *Engine) npmInstallZG(stage string, stdout, stderr io.Writer) error {
	// Empty private configs and stripped npm overrides avoid global installs,
	// user lifecycle settings, credential forwarding and a user-selected registry.
	for _, name := range []string{"npmrc", "global-npmrc"} {
		if err := os.WriteFile(filepath.Join(stage, name), nil, 0600); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cpu := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	cmd := exec.CommandContext(ctx, e.managedZGNode(), e.managedZGNPM(), "ci", "--os=linux", "--cpu="+cpu, "--libc=glibc", "--ignore-scripts", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org", "--prefix="+stage, "--cache="+filepath.Join(stage, "npm-cache"), "--userconfig="+filepath.Join(stage, "npmrc"), "--globalconfig="+filepath.Join(stage, "global-npmrc"))
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
