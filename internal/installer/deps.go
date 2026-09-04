package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		return string(b), ctx.Err()
	}
	return string(b), err
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func findTmux() (string, string, error) {
	candidates := []string{os.Getenv("CODEX_PANEL_TMUX"), "/usr/bin/tmux", "/usr/local/bin/tmux"}
	// Reuse a previous local panel installation when PATH only contains a shim.
	// This is discovered on the destination host; no source-machine path is bundled.
	if userHome, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(userHome, ".local", "share", "codex-panel", "tmux", "bin", "tmux"))
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidates = append(candidates, filepath.Join(dir, "tmux"))
	}
	seen := map[string]bool{}
	re := regexp.MustCompile(`^tmux (\d+)\.(\d+)`)
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		out, err := run(3*time.Second, candidate, "-V")
		if err != nil || strings.Contains(strings.ToLower(out), "shim") {
			continue
		}
		matches := re.FindStringSubmatch(strings.TrimSpace(out))
		if matches == nil {
			continue
		}
		major, _ := strconv.Atoi(matches[1])
		minor, _ := strconv.Atoi(matches[2])
		if major < 3 || (major == 3 && minor < 3) {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", "", err
		}
		return absolute, strings.TrimSpace(out), nil
	}
	return "", "", errors.New("falta tmux real >= 3.3 (probado: 3.7b). Instálalo o indica CODEX_PANEL_TMUX=/ruta/al/tmux; un shim no sirve")
}

func (e *Engine) preparePanel(p *Plan, put func(string, []byte, fs.FileMode) error, get func(string) (*Change, error)) error {
	python, err := exec.LookPath("python3")
	if err != nil {
		return errors.New("falta Python 3.11+ con curses; instala python3 y su módulo curses")
	}
	python, err = filepath.Abs(python)
	if err != nil {
		return err
	}
	out, err := run(5*time.Second, python, "-c", "import sys,curses,tomllib,sqlite3,fcntl; assert sys.version_info >= (3,11), 'Python >= 3.11 requerido'; assert curses.has_extended_color_support(), 'curses requiere colores extendidos'")
	if err != nil {
		return fmt.Errorf("Python/curses no compatible: %s (%w)", strings.TrimSpace(out), err)
	}
	tmux, version, err := findTmux()
	if err != nil {
		return err
	}
	tic, err := exec.LookPath("tic")
	if err != nil {
		return errors.New("falta tic (ncurses-bin en Debian/Ubuntu, ncurses en Fedora/Arch)")
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		return errors.New("falta Codex CLI en PATH; instala Codex e inicia sesión por separado (versión probada: 0.153.0)")
	}
	codex, err = filepath.Abs(codex)
	if err != nil {
		return err
	}
	codexVersion, err := run(5*time.Second, codex, "--version")
	if err != nil {
		return fmt.Errorf("codex --version falló: %s (%w)", strings.TrimSpace(codexVersion), err)
	}
	p.Warnings = append(p.Warnings, fmt.Sprintf("Panel: %s; %s. Compatibilidad probada con Codex 0.153.0 y tmux 3.7b; otras versiones requieren validación.", version, strings.TrimSpace(codexVersion)), "El panel consulta la cuota al abrirse. CODEX_PANEL_QUOTA_OFFLINE=1 lo desactiva. Las tarifas estimadas requieren revalidación después del 2026-11-21.")
	// Compile for this host before writing any destination. Only a disposable
	// directory is touched during preview; no tmux server or model is launched.
	stage, err := os.MkdirTemp("", "codex-setup-terminfo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	src, err := fs.ReadFile(e.assets, "panel/panel.terminfo")
	if err != nil {
		return err
	}
	source := filepath.Join(stage, "panel.terminfo")
	if err = os.WriteFile(source, src, 0600); err != nil {
		return err
	}
	compiled := filepath.Join(stage, "compiled")
	out, err = run(5*time.Second, tic, "-x", "-o", compiled, source)
	if err != nil {
		return fmt.Errorf("tic falló: %s (%w)", out, err)
	}
	err = filepath.WalkDir(compiled, func(filename string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(compiled, filename)
		if err != nil {
			return err
		}
		target, err := e.target("data", filepath.Join("codex-panel", "terminfo", rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		return put(target, b, 0644)
	})
	if err != nil {
		return err
	}
	script, err := e.target("data", "codex-panel/codex_panel.py")
	if err != nil {
		return err
	}
	wrapper := "#!/bin/sh\n# Generated on this host by codex-setup.\n" +
		"if [ -z \"${CODEX_HOME:-}\" ]; then export CODEX_HOME=" + shellQuote(e.CodexHome) + "; fi\n" +
		"if [ -z \"${CODEX_PANEL_TMUX:-}\" ]; then export CODEX_PANEL_TMUX=" + shellQuote(tmux) + "; fi\n" +
		"exec " + shellQuote(python) + " " + shellQuote(script) + " \"$@\"\n"
	launcher, err := e.target("bin", "codex-panel")
	if err != nil {
		return err
	}
	if err = put(launcher, []byte(wrapper), 0755); err != nil {
		return err
	}
	return e.prepareEntry(p, python, codex, launcher, put, get)
}
