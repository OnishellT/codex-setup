package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func (e *Engine) checkInstalledFiles(modules []Module) []string {
	var pending []string
	for _, m := range modules {
		for _, op := range m.Operations {
			if op.Kind != "copy" && op.Kind != "copy-if-missing" && op.Kind != "tree" && op.Kind != "template-copy" {
				continue
			}
			target, err := e.target(op.Root, op.Target)
			if err != nil {
				pending = append(pending, fmt.Sprintf("%s: %v", m.ID, err))
				continue
			}
			if err = checkPath(target); err != nil {
				pending = append(pending, fmt.Sprintf("%s: %v", m.ID, err))
				continue
			}
			if op.Kind == "tree" {
				err = fs.WalkDir(e.assets, op.Source, func(path string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if entry.IsDir() {
						return nil
					}
					rel, _ := filepath.Rel(op.Source, path)
					dst, e2 := e.target(op.Root, filepath.Join(op.Target, rel))
					if e2 != nil {
						return e2
					}
					if e2 = checkPath(dst); e2 != nil {
						return e2
					}
					info, e2 := os.Stat(dst)
					if e2 != nil || !info.Mode().IsRegular() {
						return fmt.Errorf("falta archivo de árbol %s", dst)
					}
					return nil
				})
				if err != nil {
					pending = append(pending, fmt.Sprintf("%s: %v", m.ID, err))
				}
				continue
			}
			if _, err = os.Stat(target); err != nil {
				pending = append(pending, fmt.Sprintf("%s: falta %s", m.ID, op.Target))
			}
		}
	}
	return pending
}

func (e *Engine) checkInstalledModels(status *AccountStatus, modules []Module) []string {
	if status == nil {
		return []string{"cuenta: catálogo no verificado"}
	}
	allowed := func(c ModelChoice) bool {
		for _, m := range status.Models {
			if m.Model == c.Model {
				return hasString(m.Efforts, c.Effort)
			}
		}
		return false
	}
	var pending []string
	check := func(path, label string) {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			pending = append(pending, label+": falta "+filepath.Base(path))
			return
		}
		if err != nil {
			pending = append(pending, label+": no legible")
			return
		}
		var c map[string]any
		if toml.Unmarshal(b, &c) != nil {
			pending = append(pending, label+": TOML inválido")
			return
		}
		model, _ := c["model"].(string)
		effort, _ := c["model_reasoning_effort"].(string)
		if !allowed(ModelChoice{model, effort}) {
			pending = append(pending, label+": modelo no está en catálogo")
		}
	}
	check(filepath.Join(e.CodexHome, "config.toml"), "principal")
	for _, m := range modules {
		if m.ID != "prewalk" {
			continue
		}
		for _, role := range ModelRoles {
			if role.ID == "principal" || role.ID == "default" {
				continue
			}
			check(filepath.Join(e.CodexHome, "agents", role.ID+".toml"), role.ID)
		}
	}
	return pending
}

func (e *Engine) readinessFilesAndModels(modules []Module, status *AccountStatus) []string {
	out := e.checkInstalledFiles(modules)
	out = append(out, e.checkInstalledModels(status, modules)...)
	for i := range out {
		if !strings.Contains(out[i], "vuelve") {
			out[i] += "; corrige y vuelve a comprobar"
		}
	}
	return out
}
