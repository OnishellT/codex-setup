package installer

// Dependency probing and installation deliberately live outside BuildPlan:
// preview must be completely read-only, while installation is an explicit
// second step owned by the caller.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type DependencyCommand struct {
	Path string
	Args []string
}

type DependencyPlan struct {
	Missing   []string
	Commands  []DependencyCommand
	Warnings  []string
	owner     *Engine
	signature string
	ids       []string
}

func (p *DependencyPlan) NeedsInstall() bool { return p != nil && len(p.Missing) != 0 }

var dependencyOSRelease = "/etc/os-release"
var dependencyCABundles = []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/cert.pem"}
var dependencyManagerPath = secureManagerPath

type dependencyRequirement struct {
	name, binary, version string
}

const (
	depTmux          = "tmux >= 3.3"
	depTic           = "tic (ncurses)"
	depNode18        = "node >= 18"
	depNode22        = "node >= 22"
	depPython        = "python3"
	depPythonModules = "python curses/sqlite3/tomllib/fcntl"
	depCA            = "ca-certificates"
)

func (e *Engine) PlanDependencies(ids []string) (*DependencyPlan, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("dependencias nativas soportadas solo en Linux amd64/arm64 (detectado %s)", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf("arquitectura no soportada: %s", runtime.GOARCH)
	}
	modules, err := e.Resolve(ids)
	if err != nil {
		return nil, err
	}
	reqs := selectedRequirements(modules)
	p := &DependencyPlan{owner: e, ids: append([]string(nil), ids...)}
	for _, req := range reqs {
		satisfied := dependencySatisfied(req)
		if req.binary == "rtk" {
			satisfied = e.rtkAvailable()
		}
		if satisfied {
			continue
		}
		p.Missing = append(p.Missing, req.name)
	}
	if len(p.Missing) == 0 {
		p.signature = dependencySignature(p)
		return p, nil
	}
	p.Commands, err = dependencyCommands(p.Missing)
	if err != nil {
		return nil, fmt.Errorf("faltan dependencias (%s): %w", strings.Join(p.Missing, ", "), err)
	}
	if contains(p.Missing, "rtk") {
		p.Warnings = append(p.Warnings, "RTK se descargará y verificará en CODEX_HOME/integrations/rtk/bin/rtk tras tu consentimiento.")
	}
	p.signature = dependencySignature(p)
	return p, nil
}

func dependencyCommands(missing []string) ([]DependencyCommand, error) {
	packageMissing := make([]string, 0, len(missing))
	for _, item := range missing {
		if item != "rtk" {
			packageMissing = append(packageMissing, item)
		}
	}
	if len(packageMissing) == 0 {
		return nil, nil
	}
	manager, err := packageManager()
	if err != nil {
		return nil, err
	}
	packages := manager.packages(packageMissing)
	if len(packages) == 0 {
		return nil, nil
	}
	args := append([]string{"install", "-y"}, packages...)
	if manager.family == "pacman" {
		args = append([]string{"-S", "--needed", "--noconfirm"}, packages...)
	}
	commands := []DependencyCommand{}
	if manager.family == "apt" {
		commands = append(commands, DependencyCommand{Path: manager.path, Args: []string{"update"}})
	}
	if os.Geteuid() == 0 {
		return append(commands, DependencyCommand{Path: manager.path, Args: args}), nil
	}
	sudo, err := dependencyManagerPath("sudo")
	if err != nil {
		return nil, errors.New("sudo no está disponible; instala con consentimiento explícito")
	}
	for i := range commands {
		commands[i] = DependencyCommand{Path: sudo, Args: append([]string{manager.path}, commands[i].Args...)}
	}
	return append(commands, DependencyCommand{Path: sudo, Args: append([]string{manager.path}, args...)}), nil
}

func selectedRequirements(modules []Module) []dependencyRequirement {
	seen := map[string]bool{}
	add := func(req dependencyRequirement) {
		if !seen[req.name] {
			seen[req.name] = true
		}
	}
	for _, m := range modules {
		switch m.ID {
		case "panel":
			add(dependencyRequirement{depPython, "python3", "3.11"})
			add(dependencyRequirement{depPythonModules, "python3", "3.11"})
			add(dependencyRequirement{depTmux, "tmux", "3.3"})
			add(dependencyRequirement{depTic, "tic", ""})
		case "prewalk":
			add(dependencyRequirement{depPython, "python3", "3.11"})
			add(dependencyRequirement{"git", "git", ""})
			add(dependencyRequirement{"tar", "tar", ""})
			add(dependencyRequirement{"xz", "xz", ""})
			add(dependencyRequirement{depCA, depCA, ""})
		case "ponytail":
			add(dependencyRequirement{depPython, "python3", "3.11"})
			add(dependencyRequirement{depNode18, "node", "18"})
		case "context-handoff":
			add(dependencyRequirement{depPython, "python3", "3.11"})
		case "zg":
			add(dependencyRequirement{depNode22, "node", "22"})
			add(dependencyRequirement{"npm", "npm", ""})
		case "rtk":
			add(dependencyRequirement{depPython, "python3", "3.11"})
			// RTK is managed by the installer, not the host package manager.
			add(dependencyRequirement{"rtk", "rtk", "0.23"})
			add(dependencyRequirement{depCA, depCA, ""})
		}
	}
	if seen[depNode22] {
		delete(seen, depNode18)
	}
	out := make([]dependencyRequirement, 0, len(seen))
	for _, req := range []dependencyRequirement{{depPython, "python3", "3.11"}, {"python curses/sqlite3/tomllib/fcntl", "python3", "3.11"}, {"git", "git", ""}, {"tar", "tar", ""}, {"xz", "xz", ""}, {depCA, depCA, ""}, {depTmux, "tmux", "3.3"}, {depTic, "tic", ""}, {depNode18, "node", "18"}, {depNode22, "node", "22"}, {"npm", "npm", ""}, {"rtk", "rtk", "0.23"}} {
		if seen[req.name] {
			out = append(out, req)
		}
	}
	return out
}

func dependencySatisfied(req dependencyRequirement) bool {
	if req.name == depCA {
		return certificatesAvailable()
	}
	path, err := exec.LookPath(req.binary)
	if err != nil {
		return false
	}
	if req.version == "" {
		return true
	}
	args := []string{"--version"}
	if req.binary == "tmux" {
		args = []string{"-V"}
	}
	if req.binary == "python3" {
		args = []string{"-c", "import sys,curses; assert sys.version_info >= (3,11); assert curses.has_extended_color_support()"}
		if req.name == depPythonModules {
			args = []string{"-c", "import sys,curses,sqlite3,tomllib,fcntl; assert sys.version_info >= (3,11); assert curses.has_extended_color_support()"}
		}
	}
	out, err := run(3*time.Second, path, args...)
	if err != nil {
		return false
	}
	if req.binary == "python3" {
		return true
	}
	majorText, minorText, hasMinor := strings.Cut(req.version, ".")
	if !hasMinor {
		minorText = "0"
	}
	wantMajor, err1 := strconv.Atoi(majorText)
	wantMinor, err2 := strconv.Atoi(minorText)
	if err1 != nil || err2 != nil {
		return false
	}
	return atLeastVersion(out, wantMajor, wantMinor)
}

func certificatesAvailable() bool {
	for _, filename := range dependencyCABundles {
		data, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(data) {
			return true
		}
	}
	return false
}

type packageManagerInfo struct{ path, family string }

func packageManager() (packageManagerInfo, error) {
	data, err := os.ReadFile(dependencyOSRelease)
	if err != nil {
		return packageManagerInfo{}, fmt.Errorf("no se pudo leer %s", dependencyOSRelease)
	}
	values := map[string]string{}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), "="); ok {
			values[k] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	id := strings.Fields(strings.ToLower(values["ID"] + " " + values["ID_LIKE"]))
	for _, candidate := range []struct {
		family string
		ids    []string
	}{{"apt", []string{"debian", "ubuntu", "linuxmint"}}, {"dnf", []string{"fedora", "rhel", "centos"}}, {"pacman", []string{"arch", "manjaro"}}} {
		if !anyToken(id, candidate.ids) {
			continue
		}
		command := map[string]string{"apt": "apt-get", "dnf": "dnf", "pacman": "pacman"}[candidate.family]
		path, err := dependencyManagerPath(command)
		if err == nil {
			return packageManagerInfo{path, candidate.family}, nil
		}
		return packageManagerInfo{}, fmt.Errorf("distribución %s requiere %s, pero no está disponible", values["ID"], command)
	}
	return packageManagerInfo{}, fmt.Errorf("distribución no soportada (%s); soportadas: Debian/Ubuntu, Fedora y Arch", values["ID"])
}

func secureManagerPath(name string) (string, error) {
	path := filepath.Join("/usr/bin", name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&022 != 0 {
		return "", errors.New("gestor nativo no confiable o ausente")
	}
	for dir := filepath.Dir(path); dir != "/"; dir = filepath.Dir(dir) {
		info, err = os.Stat(dir)
		if err != nil || info.Mode().Perm()&022 != 0 {
			return "", errors.New("ruta del gestor no confiable")
		}
	}
	return path, nil
}
func anyToken(tokens, wanted []string) bool {
	for _, a := range tokens {
		for _, b := range wanted {
			if a == b {
				return true
			}
		}
	}
	return false
}

func (m packageManagerInfo) packages(missing []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, item := range missing {
		for _, pkg := range m.requirementPackages(item) {
			add(pkg)
		}
	}
	return out
}

func (m packageManagerInfo) requirementPackages(item string) []string {
	switch item {
	case depPython, depPythonModules:
		if m.family == "pacman" {
			return []string{"python"}
		}
		if m.family == "apt" {
			return []string{"python3", "python3-full"}
		}
		return []string{"python3"}
	case depTmux:
		return []string{"tmux"}
	case depTic:
		if m.family == "apt" {
			return []string{"ncurses-bin"}
		}
		return []string{"ncurses"}
	case depNode18, depNode22:
		return []string{"nodejs"}
	case "npm", "git", "tar", depCA:
		return []string{item}
	case "xz":
		if m.family == "apt" {
			return []string{"xz-utils"}
		}
		return []string{"xz"}
	}
	return nil
}

func dependencySignature(p *DependencyPlan) string {
	var b strings.Builder
	b.WriteString(strings.Join(p.Missing, "\x00"))
	for _, c := range p.Commands {
		b.WriteString("\x01" + c.Path + "\x00" + strings.Join(c.Args, "\x00"))
	}
	return b.String()
}

func (e *Engine) InstallDependencies(p *DependencyPlan, stdin io.Reader, stdout, stderr io.Writer) error {
	if p == nil || p.owner != e || p.signature != dependencySignature(p) {
		return errors.New("plan de dependencias inválido o modificado")
	}
	if !p.NeedsInstall() {
		return nil
	}
	for _, c := range p.Commands {
		if !allowlistedDependencyCommand(c) {
			return errors.New("comando de dependencia no permitido")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		cmd := exec.CommandContext(ctx, c.Path, c.Args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		err := cmd.Run()
		ctxErr := ctx.Err()
		cancel()
		if ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return fmt.Errorf("falló instalación de dependencias: %w", err)
		}
	}
	if contains(p.Missing, "rtk") {
		if err := e.installRTK(stdout); err != nil {
			return err
		}
	}
	check, err := e.PlanDependencies(p.ids)
	if err != nil {
		return fmt.Errorf("no se pudieron revalidar dependencias: %w", err)
	}
	if check.NeedsInstall() {
		return fmt.Errorf("la instalación terminó, pero aún faltan: %s", strings.Join(check.Missing, ", "))
	}
	return nil
}

func allowlistedDependencyCommand(c DependencyCommand) bool {
	return c.Path != "" && (strings.HasSuffix(c.Path, "/apt-get") || strings.HasSuffix(c.Path, "/dnf") || strings.HasSuffix(c.Path, "/pacman") || strings.HasSuffix(c.Path, "/sudo"))
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
