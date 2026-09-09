package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const readinessHooksFile = "hooks.json"

func TestCheckReadinessHooksStates(t *testing.T) {
	const template = `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"python3 {{CODEX_HOME_SHELL}}/a.py","statusMessage":"[codex-setup:test] first"},{"type":"command","command":"python3 {{CODEX_HOME_SHELL}}/b.py","statusMessage":"[codex-setup:test] second"}]}]}}`
	for _, name := range []string{"ready", "missing", "foreign", "disabled", "modified-trust", "duplicate-runtime", "wrong-event", "wrong-source", "unknown-feature", "disabled-feature", "warning", "modified-command", "modified-matcher", "missing-file-handler", "duplicate-file-handler", "missing-file"} {
		t.Run(name, func(t *testing.T) {
			runHookReadinessCase(t, name, template)
		})
	}
}

func mutateHookReadiness(name string, status *AccountStatus, group map[string]any, path string) {
	handlers := group["hooks"].([]any)
	switch name {
	case "missing":
		status.Hooks = status.Hooks[:1]
	case "foreign":
		status.Hooks[1].StatusMessage = "unrelated"
	case "disabled":
		status.Hooks[1].Enabled = false
	case "modified-trust":
		status.Hooks[1].TrustStatus = "modified"
	case "duplicate-runtime":
		status.Hooks = append(status.Hooks, status.Hooks[1])
	case "wrong-event":
		status.Hooks[1].EventName = "sessionStart"
	case "wrong-source":
		status.Hooks[1].SourcePath = path + ".foreign"
	case "unknown-feature":
		status.HooksFeatureKnown = false
	case "disabled-feature":
		status.HooksEnabled = false
	case "warning":
		status.HookWarnings = []string{"unavailable"}
	case "modified-command":
		handlers[1].(map[string]any)["command"] = "other-command"
	case "modified-matcher":
		group["matcher"] = "other"
	case "missing-file-handler":
		group["hooks"] = handlers[:1]
	case "duplicate-file-handler":
		group["hooks"] = append(handlers, handlers[1])
	}
}

func runHookReadinessCase(t *testing.T, name, template string) {
	t.Helper()
	e := &Engine{CodexHome: t.TempDir(), assets: fstest.MapFS{readinessHooksFile: &fstest.MapFile{Data: []byte(template)}}, Modules: []Module{{ID: "test", Operations: []Operation{{Kind: "hooks-state", Source: readinessHooksFile, Root: "codex", Target: readinessHooksFile}}}}}
	path := filepath.Join(e.CodexHome, readinessHooksFile)
	doc := decodeJSONObject([]byte(template))
	if err := expandHookCommands(doc, shellQuote(e.CodexHome)); err != nil {
		t.Fatal(err)
	}
	group := doc["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	handlers := group["hooks"].([]any)
	status := &AccountStatus{HooksFeatureKnown: true, HooksEnabled: true, Models: []ModelOption{{Model: "test", Efforts: []string{"medium"}}}}
	for _, h := range handlers {
		status.Hooks = append(status.Hooks, HookStatus{EventName: "preToolUse", StatusMessage: h.(map[string]any)["statusMessage"].(string), SourcePath: path, Enabled: true, TrustStatus: "trusted"})
	}
	mutateHookReadiness(name, status, group, path)
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if name != "missing-file" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.CheckReadiness([]string{"test"}, status)
	if err != nil || r.Ready() != (name == "ready") {
		t.Fatalf("r=%#v err=%v", r, err)
	}
	if !r.Ready() && !strings.Contains(strings.Join(r.Pending, " "), "/hooks") {
		t.Fatal("missing actionable guidance")
	}
}
