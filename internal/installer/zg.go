package installer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const zgHelper = "integrations/zg/pr86-watch-manager.mjs"
const zgPackageVersion = "0.2.1"
const zgCLI = "dist/cli/index.js"

var zgPackageRuntimeHashes = map[string]string{
	"amd64": "c3d3c1bd1bc89648a03c33b30d7dec1a34035b8ed7695c85b6053eda4a0efc17",
	"arm64": "89157c5430286280e573df82a3940c315fb873503a8b062c4a59178cfeb15d2f",
}

func (e *Engine) managedZGPackages() string {
	return filepath.Join(e.CodexHome, "integrations", "zg", "packages-v"+zgPackageVersion)
}

func zgPackage(prefix string) string {
	return filepath.Join(prefix, "node_modules", "@zvec", "zvec-grep")
}

func (e *Engine) checkZG() error {
	if err := validateZGNode(filepath.Dir(filepath.Dir(e.managedZGNode()))); err != nil {
		return err
	}
	return e.validateZGPackage(e.managedZGPackages())
}

func (e *Engine) validateZGPackage(prefix string) error {
	digest, err := directorySHA256(filepath.Join(prefix, "node_modules"))
	if err != nil || digest != zgPackageRuntimeHashes[runtime.GOARCH] {
		return fmt.Errorf("runtime zg ausente o modificado: %s", prefix)
	}
	pkg := zgPackage(prefix)
	for _, filename := range []string{"package.json", zgCLI, "dist/daemon/watch-manager.js"} {
		path := filepath.Join(pkg, filename)
		if err := checkPath(path); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("zg: falta archivo privado %s", filename)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(pkg, "package.json"))
	if err != nil {
		return err
	}
	var meta struct {
		Name, Version string
		Bin           map[string]string
	}
	if json.Unmarshal(manifest, &meta) != nil || meta.Name != "@zvec/zvec-grep" || meta.Version != zgPackageVersion || meta.Bin["zg"] != zgCLI {
		return fmt.Errorf("zg: paquete privado no reconocido")
	}
	if err := e.zgPatch("check", pkg); err != nil {
		return err
	}
	// Imports and rg --version exercise native components without models or indices.
	const check = `import {createRequire} from 'node:module'; import {pathToFileURL} from 'node:url'; import {accessSync,constants} from 'node:fs'; import {execFileSync} from 'node:child_process'; const require=createRequire(process.argv[1]+'/package.json'); for (const name of ['@zvec/zvec','onnxruntime-node','@huggingface/transformers']) await import(pathToFileURL(require.resolve(name))); const {rgPath}=require('@vscode/ripgrep'); accessSync(rgPath,constants.X_OK); execFileSync(rgPath,['--version']); console.log('ready');`
	out, err := run(15*time.Second, e.managedZGNode(), "--input-type=module", "--eval", check, pkg)
	if err != nil || strings.TrimSpace(out) != "ready" {
		return fmt.Errorf("zg: componentes nativos no disponibles: %s", strings.TrimSpace(out))
	}
	return nil
}

func (e *Engine) zgPatch(action, pkg string) error {
	helper, err := fs.ReadFile(e.assets, zgHelper)
	if err != nil {
		return err
	}
	out, err := run(3*time.Second, e.managedZGNode(), "--input-type=module", "--eval", string(helper)+"\nconsole.log(await run(process.argv[1], process.argv[2]));", action, pkg)
	if err != nil || strings.TrimSpace(out) != "patched" {
		return fmt.Errorf("zg: parche Linux PR 86 no verificado: %s", strings.TrimSpace(out))
	}
	return nil
}
