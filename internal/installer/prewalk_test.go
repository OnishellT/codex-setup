package installer_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex-setup/internal/installer"
)

func TestPrewalkSettingsMigrateOnlyGeneratedDefaults(t *testing.T) {
	modules := []installer.Module{{ID: "prewalk", Operations: []installer.Operation{{Kind: "prewalk-settings", Root: "codex", Target: "integrations/prewalk/settings.json", Source: "settings.json"}}}}
	engine, _ := testEngine(t, modules, map[string]string{"settings.json": "{\"worktrees\":true,\"max_workers\":4,\"quality\":true}\n"})
	filename := filepath.Join(engine.CodexHome, "integrations/prewalk/settings.json")
	writeFixture(t, filename, "{\"worktrees\":true,\"max_workers\":2}\n", 0600)
	applyPlan(t, engine, buildPlan(t, engine, "prewalk"))
	var got map[string]any
	if err := json.Unmarshal(readFixture(t, filename), &got); err != nil {
		t.Fatal(err)
	}
	if got["max_workers"] != float64(4) || got["quality"] != true {
		t.Fatalf("generated settings were not migrated: %#v", got)
	}
	assertIdempotent(t, engine, "prewalk")

	writeFixture(t, filename, "{\"worktrees\":true,\"max_workers\":1,\"custom\":true}\n", 0600)
	plan := buildPlan(t, engine, "prewalk")
	if len(plan.Changes) != 0 || !strings.Contains(strings.Join(plan.Warnings, "\n"), "settings.json personalizado") {
		t.Fatalf("custom settings should be preserved with warning: %#v, %#v", plan.Changes, plan.Warnings)
	}
}

func TestAgentInstructionsMigrateKnownVersionAndPreserveCustomFields(t *testing.T) {
	modules := []installer.Module{{ID: "prewalk", Operations: []installer.Operation{{Kind: "agent-instructions", Root: "codex", Target: "agents/prewalk_executor.toml", Source: "executor.toml"}}}}
	engine, _ := testEngine(t, modules, map[string]string{"executor.toml": `name = "prewalk_executor"
model = "gpt-5.6-terra"
developer_instructions = "new instructions"
[agents]
enabled = false
`})
	filename := filepath.Join(engine.CodexHome, "agents/prewalk_executor.toml")
	old := `Ejecuta únicamente el encargo y contrato entregados por el agente principal.
Antes de editar, revisa el alcance, archivos asignados, cambios locales y criterios de aceptación. Conserva los cambios ajenos y no amplíes ni rediseñes el trabajo silenciosamente.

Realiza las validaciones solicitadas y devuelve un resumen conciso: cambios, comandos ejecutados y resultado, incertidumbres y bloqueos. Detente y devuelve un bloqueo concreto si falta autorización, cambian las premisas, hay riesgo de seguridad o el alcance debe cambiar.

No delegues, no inicies Codex ni otros agentes anidados, y no uses procesos externos para eludir permisos. El agente principal conserva las decisiones, integración y comunicación final.
`
	writeFixture(t, filename, "model = 'custom-model'\nmodel_provider = 'custom-provider'\nsandbox_mode = 'workspace-write'\ncustom = 'keep'\ndeveloper_instructions = '''\n"+old+"'''\n", 0600)
	applyPlan(t, engine, buildPlan(t, engine, "prewalk"))
	got := readTOML(t, filename)
	if got["model"] != "custom-model" || got["model_provider"] != "custom-provider" || got["sandbox_mode"] != "workspace-write" || got["custom"] != "keep" || got["developer_instructions"] != "new instructions" {
		t.Fatalf("known agent migration did not preserve custom fields: %#v", got)
	}
	assertIdempotent(t, engine, "prewalk")
}

func TestGeneratedWriterPermissionDefaultsDoNotReplaceUserChoices(t *testing.T) {
	modules := []installer.Module{{ID: "prewalk", Operations: []installer.Operation{{Kind: "agent-instructions", Root: "codex", Target: "agents/prewalk_executor.toml", Source: "executor.toml"}}}}
	engine, _ := testEngine(t, modules, map[string]string{"executor.toml": `name = "prewalk_executor"
sandbox_mode = "workspace-write"
approval_policy = "on-request"
approvals_reviewer = "auto_review"
developer_instructions = "new instructions"
`})
	filename := filepath.Join(engine.CodexHome, "agents/prewalk_executor.toml")
	writeFixture(t, filename, `name = "prewalk_executor"
developer_instructions = '''
Ejecuta únicamente el encargo y contrato entregados por el agente principal.
Antes de editar, revisa el alcance, archivos asignados, cambios locales y criterios de aceptación. Conserva los cambios ajenos y no amplíes ni rediseñes el trabajo silenciosamente.

Realiza las validaciones solicitadas y devuelve un resumen conciso: cambios, comandos ejecutados y resultado, incertidumbres y bloqueos. Detente y devuelve un bloqueo concreto si falta autorización, cambian las premisas, hay riesgo de seguridad o el alcance debe cambiar.

No delegues, no inicies Codex ni otros agentes anidados, y no uses procesos externos para eludir permisos. El agente principal conserva las decisiones, integración y comunicación final.
'''
`, 0600)
	applyPlan(t, engine, buildPlan(t, engine, "prewalk"))
	got := readTOML(t, filename)
	if got["sandbox_mode"] != "workspace-write" || got["approval_policy"] != "on-request" || got["approvals_reviewer"] != "auto_review" {
		t.Fatalf("generated writer defaults were not installed: %#v", got)
	}

	writeFixture(t, filename, `name = "prewalk_executor"
sandbox_mode = "danger-full-access"
approval_policy = "never"
approvals_reviewer = "user"
developer_instructions = "new instructions"
`, 0600)
	plan := buildPlan(t, engine, "prewalk")
	if len(plan.Changes) != 0 {
		t.Fatalf("user permission choices were overwritten: %#v", plan.Changes)
	}
}

func TestNativeDescriptionMigrationPreservesCustomDescriptions(t *testing.T) {
	e, _ := testEngine(t, []installer.Module{{ID: "prewalk", Operations: []installer.Operation{{Kind: "agent-instructions", Root: "codex", Target: "agents/fallback_explorer.toml", Source: "role.toml"}}}}, map[string]string{"role.toml": "developer_instructions='managed'\ndescription='Native fallback'\n"})
	path := filepath.Join(e.CodexHome, "agents/fallback_explorer.toml")
	for _, description := range []string{"Explorador read-only para búsquedas simples o masivas y fallback si Muse no está disponible.", "My custom role"} {
		writeFixture(t, path, "developer_instructions='managed'\ndescription='"+description+"'\n", 0600)
		applyPlan(t, e, buildPlan(t, e, "prewalk"))
		want := description
		if description != "My custom role" {
			want = "Native fallback"
		}
		if readTOML(t, path)["description"] != want {
			t.Fatal("incorrect description migration")
		}
	}
}

func TestAgentInstructionsPreserveCustomTextAndRemoveOnlyLegacyReviewerHook(t *testing.T) {
	modules := []installer.Module{{ID: "prewalk", Operations: []installer.Operation{{Kind: "agent-instructions", Root: "codex", Target: "agents/engineering_reviewer.toml", Source: "reviewer.toml"}}}}
	engine, _ := testEngine(t, modules, map[string]string{"reviewer.toml": `name = "engineering_reviewer"
developer_instructions = "setup instructions {{CODEX_HOME_SHELL}}"
[agents]
enabled = false
`})
	filename := filepath.Join(engine.CodexHome, "agents/engineering_reviewer.toml")
	writeFixture(t, filename, `model = "custom-model"
model_provider = "custom-provider"
developer_instructions = "my review rules"
[[hooks.PreToolUse]]
matcher = "*"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "foreign"
statusMessage = "foreign handler"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "old"
statusMessage = "[codex-setup:prewalk-review] old"
`, 0600)
	plan := buildPlan(t, engine, "prewalk")
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "instrucciones personalizadas") {
		t.Fatalf("custom instructions must require manual review: %#v", plan.Warnings)
	}
	applyPlan(t, engine, plan)
	got := string(readFixture(t, filename))
	for _, want := range []string{"my review rules", "custom-model", "foreign handler"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reviewer merge missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[codex-setup:prewalk-review]") || strings.Contains(got, "{{CODEX_HOME_SHELL}}") {
		t.Fatalf("legacy reviewer hook was not safely removed:\n%s", got)
	}
}
