package installer

import "fmt"

// VerifyInstallation observes installed state, including native trust; it never
// applies a plan, grants trust, downloads dependencies, or creates search indices.
func (e *Engine) VerifyInstallation(ids []string) (*Readiness, error) {
	status, accountErr := e.ReadAccount()
	r, err := e.CheckReadiness(ids, status)
	if err != nil {
		return nil, err
	}
	if accountErr != nil {
		r.Pending = append(r.Pending, fmt.Sprintf("cuenta: %v; ejecuta codex login y vuelve a comprobar", accountErr))
	}
	dependencies, err := e.PlanDependencies(ids)
	if err != nil {
		r.Pending = append(r.Pending, err.Error())
	} else {
		for _, missing := range dependencies.Missing {
			r.Pending = append(r.Pending, "dependencia pendiente: "+missing)
		}
	}
	return r, nil
}
