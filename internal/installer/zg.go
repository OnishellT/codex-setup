package installer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const zgHelper = "integrations/zg/pr86-watch-manager.mjs"

func (e *Engine) checkZG() error {
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("zg requiere Node.js >= 22; instálalo antes de seleccionar el módulo")
	}
	version, err := run(3*time.Second, node, "--version")
	if err != nil || nodeMajor(version) < 22 {
		return fmt.Errorf("zg requiere Node.js >= 22 (detectado %q)", strings.TrimSpace(version))
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("zg requiere npm y @zvec/zvec-grep@0.2.1 instalados globalmente")
	}
	root, err := run(3*time.Second, npm, "root", "-g")
	if err != nil || strings.TrimSpace(root) == "" {
		return fmt.Errorf("no se pudo localizar npm global para zg: %s", strings.TrimSpace(root))
	}
	pkg := filepath.Join(strings.TrimSpace(root), "@zvec", "zvec-grep")
	pkg, err = filepath.EvalSymlinks(pkg)
	if err != nil {
		return fmt.Errorf("falta @zvec/zvec-grep@0.2.1 en npm global (%s)", pkg)
	}
	manifest, err := os.ReadFile(filepath.Join(pkg, "package.json"))
	if err != nil {
		return fmt.Errorf("falta @zvec/zvec-grep@0.2.1 en npm global (%s)", pkg)
	}
	var meta struct {
		Name, Version string
		Bin           map[string]string
	}
	if err = json.Unmarshal(manifest, &meta); err != nil || meta.Name != "@zvec/zvec-grep" || meta.Version != "0.2.1" || meta.Bin["zg"] == "" {
		return fmt.Errorf("zg requiere @zvec/zvec-grep@0.2.1 en npm global (%s)", pkg)
	}
	zg, err := exec.LookPath("zg")
	if err != nil {
		return fmt.Errorf("falta el comando zg del paquete @zvec/zvec-grep@0.2.1")
	}
	resolved, err := filepath.EvalSymlinks(zg)
	expected, expectedErr := filepath.EvalSymlinks(filepath.Join(pkg, meta.Bin["zg"]))
	if err != nil || expectedErr != nil || resolved != expected {
		return fmt.Errorf("zg en PATH no pertenece al paquete global validado @zvec/zvec-grep@0.2.1")
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	helper, err := fs.ReadFile(e.assets, zgHelper)
	if err != nil {
		return err
	}
	out, err := run(3*time.Second, node, "--input-type=module", "--eval", string(helper)+"\nconsole.log(await run(\"check\", process.argv[1]));", pkg)
	if err != nil || strings.TrimSpace(out) != "patched" {
		return fmt.Errorf("zg en Linux requiere el parche PR 86 verificado; ejecuta check/apply con %s antes de seleccionar el módulo (%s)", zgHelper, strings.TrimSpace(out))
	}
	return nil
}

func nodeMajor(version string) int {
	major := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")[0]
	n, _ := strconv.Atoi(major)
	return n
}
