package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Operation struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Root    string `json:"root"`
	Target  string `json:"target"`
	Enabled bool   `json:"enabled"`
}
type Module struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Default     bool        `json:"default"`
	Depends     []string    `json:"depends"`
	Platforms   []string    `json:"platforms"`
	Operations  []Operation `json:"operations"`
}
type Engine struct {
	Modules         []Module
	Home, CodexHome string
	assets          fs.FS
	modelChoices    map[string]ModelChoice
}
type Change struct {
	Path, Kind         string
	data, original     []byte
	mode, originalMode fs.FileMode
	existed            bool
}
type Plan struct {
	Modules  []Module
	Changes  []Change
	Warnings []string
	Models   map[string]ModelChoice
	owner    *Engine
	actions  []postAction
}
type Result struct {
	BackupDir string
	Changed   int
}

type postAction struct {
	Kind string
	Path string
}

func New(assets fs.FS, home, codexHome string) (*Engine, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	var err error
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	if home == string(filepath.Separator) {
		return nil, errors.New("el destino no puede ser la raíz del sistema")
	}
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	codexHome, err = filepath.Abs(codexHome)
	if err != nil {
		return nil, err
	}
	if codexHome == string(filepath.Separator) {
		return nil, errors.New("CODEX_HOME no puede ser la raíz del sistema")
	}
	e := &Engine{Home: home, CodexHome: codexHome, assets: assets}
	b, err := fs.ReadFile(assets, "modules.json")
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &e.Modules); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, m := range e.Modules {
		if m.ID == "" || seen[m.ID] {
			return nil, fmt.Errorf("ID de módulo inválido/duplicado: %q", m.ID)
		}
		seen[m.ID] = true
	}
	all := []string{}
	for _, m := range e.Modules {
		all = append(all, m.ID)
	}
	// Validate dependency graph without enforcing platform until selection.
	if _, err = e.resolve(all, false); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Engine) Resolve(ids []string) ([]Module, error) { return e.resolve(ids, true) }
func (e *Engine) resolve(ids []string, checkOS bool) ([]Module, error) {
	byID := map[string]Module{}
	for _, m := range e.Modules {
		byID[m.ID] = m
	}
	states := map[string]int{}
	out := []Module{}
	var visit func(string) error
	visit = func(id string) error {
		m, ok := byID[id]
		if !ok {
			return fmt.Errorf("módulo desconocido: %s", id)
		}
		if states[id] == 2 {
			return nil
		}
		if states[id] == 1 {
			return fmt.Errorf("dependencia circular: %s", id)
		}
		if checkOS && len(m.Platforms) > 0 {
			supported := false
			for _, p := range m.Platforms {
				supported = supported || p == runtime.GOOS
			}
			if !supported {
				return fmt.Errorf("%s no soporta %s (disponible: %s)", m.Name, runtime.GOOS, strings.Join(m.Platforms, ", "))
			}
		}
		states[id] = 1
		for _, d := range m.Depends {
			if err := visit(d); err != nil {
				return err
			}
		}
		states[id] = 2
		out = append(out, m)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (e *Engine) root(name string) (string, error) {
	switch name {
	case "home":
		return e.Home, nil
	case "codex":
		return e.CodexHome, nil
	case "skills":
		return filepath.Join(e.CodexHome, "skills"), nil
	case "data":
		return filepath.Join(e.Home, ".local", "share"), nil
	case "bin":
		return filepath.Join(e.Home, ".local", "bin"), nil
	}
	return "", fmt.Errorf("raíz desconocida: %s", name)
}
func (e *Engine) target(root, relative string) (string, error) {
	base, err := e.root(root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(relative) || relative == "" {
		return "", fmt.Errorf("ruta relativa inválida: %q", relative)
	}
	full := filepath.Join(base, filepath.FromSlash(relative))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ruta fuera del destino: %s", relative)
	}
	if err = checkPath(full); err != nil {
		return "", err
	}
	return full, nil
}

// Refuse symlink destinations/ancestors rather than overwrite unrelated trees.
func checkPath(p string) error {
	for current := p; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destino con enlace simbólico; revisa manualmente: %s", current)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}

func (e *Engine) BuildPlan(ids []string) (*Plan, error) {
	modules, err := e.Resolve(ids)
	if err != nil {
		return nil, err
	}
	if len(modules) == 0 {
		return nil, errors.New("selecciona al menos un módulo")
	}
	for _, m := range modules {
		if m.ID == "zg" {
			if err := e.checkZG(); err != nil {
				return nil, err
			}
			break
		}
	}
	p := &Plan{Modules: modules, owner: e}
	changes := map[string]*Change{}
	get := func(filename string) (*Change, error) {
		if c, ok := changes[filename]; ok {
			return c, nil
		}
		if err := checkPath(filename); err != nil {
			return nil, err
		}
		c := &Change{Path: filename, Kind: "crear", mode: 0600}
		info, err := os.Stat(filename)
		if err == nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("no es un archivo normal: %s", filename)
			}
			c.original, err = os.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			c.data = bytes.Clone(c.original)
			c.existed = true
			c.mode = writableMode(info.Mode())
			c.originalMode = c.mode
			c.Kind = "actualizar"
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		changes[filename] = c
		return c, nil
	}
	put := func(filename string, b []byte, mode fs.FileMode) error {
		c, err := get(filename)
		if err != nil {
			return err
		}
		c.data = b
		if !c.existed || mode&0111 != 0 {
			c.mode = mode
		}
		return nil
	}
	var merge func(string, []byte) error
	merge = func(filename string, b []byte) error {
		c, err := get(filename)
		if err != nil {
			return err
		}
		original := map[string]any{}
		addition := map[string]any{}
		if err = toml.Unmarshal(c.data, &original); err != nil {
			return fmt.Errorf("TOML existente inválido %s: %w", filename, err)
		}
		if err = toml.Unmarshal(b, &addition); err != nil {
			return err
		}
		expandConfigPaths(addition, e.CodexHome)
		before := fmt.Sprintf("%#v", original)
		mergeMap(original, addition)
		if before == fmt.Sprintf("%#v", original) && c.existed {
			return nil
		}
		// Preserve comment-only profile templates when creating them.
		if len(original) == 0 && !c.existed {
			c.data = b
			return nil
		}
		c.data, err = toml.Marshal(original)
		return err
	}
	hooksRequested := false
	nativeRequested := false
	for _, m := range modules {
		for _, op := range m.Operations {
			if op.Kind == "panel" {
				if err = e.preparePanel(p, put, get); err != nil {
					return nil, err
				}
				continue
			}
			target, err := e.target(op.Root, op.Target)
			if err != nil {
				return nil, err
			}
			switch op.Kind {
			case "copy", "copy-if-missing", "template-copy", "append", "merge", "developer-instructions", "agent-instructions", "prewalk-settings", "prewalk-config":
				b, err := fs.ReadFile(e.assets, op.Source)
				if err != nil {
					return nil, err
				}
				switch op.Kind {
				case "copy", "copy-if-missing":
					if op.Kind == "copy-if-missing" {
						c, er := get(target)
						if er != nil {
							return nil, er
						}
						if c.existed {
							continue
						}
					}
					err = put(target, b, 0644)
				case "template-copy":
					err = put(target, expandCodexHomeShell(b, e.CodexHome), 0644)
				case "merge":
					err = merge(target, b)
				case "developer-instructions":
					c, er := get(target)
					if er != nil {
						return nil, er
					}
					settings := map[string]any{}
					if er = toml.Unmarshal(c.data, &settings); er != nil {
						return nil, er
					}
					current, ok := settings["developer_instructions"].(string)
					if !ok && settings["developer_instructions"] != nil {
						return nil, errors.New("developer_instructions debe ser texto")
					}
					updated, er := managedBlock([]byte(current), expandCodexHomeShell(b, e.CodexHome), m.ID)
					if er != nil {
						return nil, er
					}
					fragment, er := toml.Marshal(map[string]any{"developer_instructions": string(updated)})
					if er != nil {
						return nil, er
					}
					err = merge(target, fragment)
				case "append":
					c, er := get(target)
					if er != nil {
						return nil, er
					}
					c.data, err = managedBlock(c.data, b, m.ID)
				case "agent-instructions":
					err = e.agentInstructions(p, target, b, get)
				case "prewalk-settings":
					err = e.prewalkSettings(p, target, b, get)
				case "prewalk-config":
					err = e.prewalkConfig(p, target, b, get)
				}
				if err != nil {
					return nil, err
				}
			case "tree":
				err = fs.WalkDir(e.assets, op.Source, func(src string, d fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if d.IsDir() {
						return nil
					}
					if d.Type()&fs.ModeSymlink != 0 {
						return fmt.Errorf("enlace en payload: %s", src)
					}
					rel := strings.TrimPrefix(src, op.Source+"/")
					dst, er := e.target(op.Root, path.Join(op.Target, rel))
					if er != nil {
						return er
					}
					b, er := fs.ReadFile(e.assets, src)
					if er != nil {
						return er
					}
					mode := fs.FileMode(0644)
					if strings.HasSuffix(src, ".sh") || bytes.HasPrefix(b, []byte("#!")) {
						mode = 0755
					}
					return put(dst, b, mode)
				})
				if err != nil {
					return nil, err
				}
			case "skills-state":
				cfg, er := e.target("codex", "config.toml")
				if er != nil {
					return nil, er
				}
				c, er := get(cfg)
				if er != nil {
					return nil, er
				}
				var settings map[string]any
				if er = toml.Unmarshal(c.data, &settings); er != nil {
					return nil, er
				}
				if settings == nil {
					settings = map[string]any{}
				}
				table, ok := settings["skills"].(map[string]any)
				if !ok && settings["skills"] != nil {
					return nil, errors.New("skills debe ser una tabla TOML")
				}
				if table == nil {
					table = map[string]any{}
					settings["skills"] = table
				}
				entries := []map[string]any{}
				if raw, ok := table["config"]; ok {
					switch a := raw.(type) {
					case []any:
						for _, v := range a {
							x, ok := v.(map[string]any)
							if !ok {
								return nil, errors.New("skills.config inválido")
							}
							entries = append(entries, x)
						}
					case []map[string]any:
						entries = a
					default:
						return nil, errors.New("skills.config debe ser una lista")
					}
				}
				er = fs.WalkDir(e.assets, op.Source, func(src string, d fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if d.IsDir() || d.Name() != "SKILL.md" {
						return nil
					}
					rel := strings.TrimPrefix(src, op.Source+"/")
					dst, err := e.target(op.Root, path.Join(op.Target, rel))
					if err != nil {
						return err
					}
					found := false
					for _, entry := range entries {
						if entry["path"] == dst || entry["path"] == filepath.Dir(dst) {
							entry["enabled"] = op.Enabled
							found = true
						}
					}
					if !found {
						entries = append(entries, map[string]any{"path": dst, "enabled": op.Enabled})
					}
					return nil
				})
				if er != nil {
					return nil, er
				}
				table["config"] = entries
				c.data, er = toml.Marshal(settings)
				if er != nil {
					return nil, er
				}
			case "hooks-state":
				hooksRequested = true
				if err = e.mergeHooks(p, m.ID, op.Source, get); err != nil {
					return nil, err
				}
			case "qlty-install":
				if err = e.planQltyInstall(target, get, put); err != nil {
					return nil, err
				}
			case "native-config":
				nativeRequested = true
			default:
				return nil, fmt.Errorf("operación desconocida: %s", op.Kind)
			}
		}
		if m.ID == "prewalk" {
			p.Warnings = append(p.Warnings, prewalkHookWarnings()...)
		}
	}
	// TOML operations can arrive in any selected-module order. Apply the single
	// hooks feature flag after all of them so an older base fragment cannot turn
	// it back off in the same atomic installation plan.
	if nativeRequested {
		if err = e.nativeConfig(p, get); err != nil {
			return nil, err
		}
	}
	if hooksRequested {
		if err = e.enableHooksFeature(get); err != nil {
			return nil, err
		}
	}
	for _, c := range changes {
		if c.existed && bytes.Equal(c.original, c.data) && c.mode == c.originalMode {
			continue
		}
		p.Changes = append(p.Changes, *c)
	}
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Path < p.Changes[j].Path })
	p.Warnings = append(p.Warnings, "Se respaldan los archivos reemplazados. La combinación TOML conserva valores ajenos, pero puede reformatear y quitar comentarios.", "No se copian credenciales, conversaciones, confianza de proyectos ni sesiones tmux. Inicia sesión en Codex por separado.")
	return p, nil
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if child, ok := v.(map[string]any); ok {
			if current, ok := dst[k].(map[string]any); ok {
				mergeMap(current, child)
				continue
			}
		}
		dst[k] = v
	}
}

func expandConfigPaths(value any, codexHome string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if text, ok := child.(string); ok {
				value[key] = strings.ReplaceAll(text, "{{CODEX_HOME}}", codexHome)
				continue
			}
			expandConfigPaths(child, codexHome)
		}
	case []any:
		for i, child := range value {
			if text, ok := child.(string); ok {
				value[i] = strings.ReplaceAll(text, "{{CODEX_HOME}}", codexHome)
				continue
			}
			expandConfigPaths(child, codexHome)
		}
	case []string:
		for i, child := range value {
			value[i] = strings.ReplaceAll(child, "{{CODEX_HOME}}", codexHome)
		}
	}
}

func managedBlock(existing, addition []byte, id string) ([]byte, error) {
	text := string(existing)
	body := strings.TrimSpace(string(addition))
	start := "<!-- codex-setup:" + id + " -->"
	end := "<!-- /codex-setup:" + id + " -->"
	block := start + "\n" + body + "\n" + end
	a, b := strings.Index(text, start), strings.Index(text, end)
	if strings.Count(text, start) > 1 || strings.Count(text, end) > 1 || ((a < 0) != (b < 0)) || (a >= 0 && b < a) {
		return nil, errors.New("bloque de instrucciones incompleto/duplicado; revisa antes de instalar")
	}
	if a >= 0 {
		prefix := strings.TrimSpace(text[:a])
		if legacyInstructionPrefixHashes[id][fmt.Sprintf("%x", sha256.Sum256([]byte(prefix)))] {
			text = text[a:]
			a = 0
			b = strings.Index(text, end)
		}
		return []byte(text[:a] + block + text[b+len(end):]), nil
	}
	if strings.Contains(text, body) {
		return existing, nil
	}
	if strings.TrimSpace(text) == "" {
		return []byte(block + "\n"), nil
	}
	return []byte(strings.TrimRight(text, "\n") + "\n\n" + block + "\n"), nil
}

var legacyInstructionPrefixHashes = map[string]map[string]bool{
	"agents": {
		// Delegation policy installed before managed markers were introduced.
		"f922773a91ff609335ffaee2a55bae90506047fa81f71c6b261d2b482a28b603": true,
	},
}

type backupEntry struct {
	Path    string `json:"path"`
	File    string `json:"file,omitempty"`
	Existed bool   `json:"existed"`
	Mode    uint32 `json:"mode"`
}

func (e *Engine) Apply(p *Plan, progress func(string)) (Result, error) {
	result := Result{}
	if p == nil || p.owner != e {
		return result, errors.New("plan no pertenece a este instalador")
	}
	if progress == nil {
		progress = func(string) {}
	}
	// Recheck the entire plan before any mutation. Do not clobber edits since preview.
	for _, c := range p.Changes {
		if err := unchanged(c); err != nil {
			return result, err
		}
	}
	if len(p.Changes) == 0 && len(p.actions) == 0 {
		return result, nil
	}
	backupRoot := filepath.Join(e.Home, ".local", "state", "codex-setup", "backups")
	if err := checkPath(backupRoot); err != nil {
		return result, err
	}
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return result, err
	}
	dir, err := os.MkdirTemp(backupRoot, time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return result, err
	}
	result.BackupDir = dir
	journal := []backupEntry{}
	for i, c := range p.Changes {
		entry := backupEntry{Path: c.Path, Existed: c.existed, Mode: uint32(c.originalMode)}
		if c.existed {
			entry.File = fmt.Sprintf("%06d.original", i)
			if err = os.WriteFile(filepath.Join(dir, entry.File), c.original, 0600); err != nil {
				return result, err
			}
		}
		journal = append(journal, entry)
	}
	b, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
		return result, err
	}
	applied := []Change{}
	rollback := func(cause error) (Result, error) {
		var failures []string
		for i := len(applied) - 1; i >= 0; i-- {
			c := applied[i]
			var er error
			if c.existed {
				er = atomicWrite(c.Path, c.original, c.originalMode)
			} else {
				er = os.Remove(c.Path)
			}
			if er != nil {
				failures = append(failures, er.Error())
			}
		}
		result.Changed = 0
		if len(failures) > 0 {
			return result, fmt.Errorf("%w; restauración incompleta: %s; respaldo: %s", cause, strings.Join(failures, "; "), dir)
		}
		return result, fmt.Errorf("%w; cambios restaurados; respaldo: %s", cause, dir)
	}
	for _, c := range p.Changes {
		progress(c.Kind + " " + c.Path)
		if err = unchanged(c); err != nil {
			return rollback(err)
		}
		if err = atomicWrite(c.Path, c.data, c.mode); err != nil {
			return rollback(err)
		}
		applied = append(applied, c)
		result.Changed++
	}
	for _, action := range p.actions {
		progress("configurar " + action.Path)
		output, actionErr := e.runAction(action)
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line != "" {
				progress(line)
			}
		}
		if actionErr != nil {
			return rollback(actionErr)
		}
	}
	progress(fmt.Sprintf("Listo: %d archivos; respaldo: %s", result.Changed, dir))
	return result, nil
}

func (e *Engine) runAction(action postAction) ([]byte, error) {
	if action.Kind == "ensure-private-dir" {
		return nil, ensurePrivateDir(action.Path)
	}
	return nil, fmt.Errorf("acción desconocida: %s", action.Kind)
}

func ensurePrivateDir(dir string) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err = os.Stat(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("destino no es un directorio: %s", dir)
	}
	if info.Mode().Perm() != 0700 {
		if err = os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func unchanged(c Change) error {
	if err := checkPath(c.Path); err != nil {
		return err
	}
	b, err := os.ReadFile(c.Path)
	if !c.existed && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("el destino cambió desde la vista previa: %s: %w", c.Path, err)
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		return err
	}
	if !c.existed || !bytes.Equal(b, c.original) || writableMode(info.Mode()) != c.originalMode {
		return fmt.Errorf("el destino cambió desde la vista previa: %s", c.Path)
	}
	return nil
}

func writableMode(mode fs.FileMode) fs.FileMode {
	return mode.Perm() | mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)
}

func atomicWrite(filename string, b []byte, mode fs.FileMode) error {
	if err := checkPath(filename); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(filename), ".codex-setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filename)
}
