package installer

import "testing"

func TestCheckReadinessHooksStates(t *testing.T) {
	e := &Engine{Modules: []Module{{ID: "hooks", Operations: []Operation{{Kind: "hooks-state"}}}}}
	for _, tc := range []struct {
		name   string
		status *AccountStatus
		ready  bool
	}{{"ready", &AccountStatus{HooksFeatureKnown: true, HooksEnabled: true, Hooks: []HookStatus{{Enabled: true, TrustStatus: "trusted"}}}, true}, {"modified", &AccountStatus{HooksFeatureKnown: true, HooksEnabled: true, Hooks: []HookStatus{{Enabled: true, TrustStatus: "modified"}}}, false}, {"foreign", &AccountStatus{HooksFeatureKnown: true, HooksEnabled: true}, false}, {"feature", &AccountStatus{HooksFeatureKnown: true}, false}, {"unknown", &AccountStatus{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := e.CheckReadiness([]string{"hooks"}, tc.status)
			if err != nil || r.Ready() != tc.ready {
				t.Fatalf("r=%#v err=%v", r, err)
			}
		})
	}
}
