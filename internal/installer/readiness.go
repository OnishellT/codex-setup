package installer

import (
	"fmt"
	"strings"
)

type Readiness struct{ Pending []string }

func (r *Readiness) Ready() bool { return r != nil && len(r.Pending) == 0 }

// CheckReadiness performs the safe hook portion of post-install verification.
func (e *Engine) CheckReadiness(ids []string, status *AccountStatus) (*Readiness, error) {
	modules, err := e.Resolve(ids)
	if err != nil {
		return nil, err
	}
	r := &Readiness{}
	for _, m := range modules {
		if !moduleNeedsHooks(m) {
			continue
		}
		if status == nil || !status.HooksFeatureKnown || !status.HooksEnabled {
			r.Pending = append(r.Pending, fmt.Sprintf("%s: habilita hooks y vuelve a comprobar (/hooks)", m.ID))
			continue
		}
		trusted := false
		seen := map[string]bool{}
		for _, h := range status.Hooks {
			if strings.Contains(h.StatusMessage, "codex-setup:"+m.ID) {
				if seen[h.StatusMessage] {
					trusted = false
					break
				}
				seen[h.StatusMessage] = true
			}
			if h.Enabled && (h.TrustStatus == "trusted" || h.TrustStatus == "managed") {
				trusted = true
				break
			}
		}
		if !trusted {
			r.Pending = append(r.Pending, fmt.Sprintf("%s: confía los hooks en /hooks y vuelve a comprobar", m.ID))
		}
	}
	return r, nil
}
func moduleNeedsHooks(m Module) bool {
	for _, op := range m.Operations {
		if op.Kind == "hooks-state" {
			return true
		}
	}
	return false
}
