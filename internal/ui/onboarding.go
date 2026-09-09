package ui

import (
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
	"codex-setup/internal/installer"
)

type accountMsg struct {
	status *installer.AccountStatus
	err    error
}
type dependencyResultMsg struct{ err error }
type verificationMsg struct {
	readiness *installer.Readiness
	err       error
}

func (m *model) accountCommand() tea.Cmd {
	engine := m.engine
	return func() tea.Msg { status, err := engine.ReadAccount(); return accountMsg{status, err} }
}

func (m *model) setAccount(status *installer.AccountStatus, err error) {
	if status == nil && err == nil {
		return
	}
	m.accountErr = err
	m.modelOptions = nil
	if status != nil {
		m.modelOptions = status.Models
	}
}

func (m *model) preparePlan() tea.Cmd {
	m.stage, m.building, m.offset = preview, true, 0
	m.plan, m.planErr, m.dependencies, m.notice = nil, nil, nil, ""
	m.generation++
	ids, generation, engine := m.ids(), m.generation, m.engine
	choices := make(map[string]installer.ModelChoice, len(m.requested))
	for role, choice := range m.requested {
		choices[role] = choice
	}
	return func() tea.Msg {
		msg := planMsg{generation: generation}
		if len(ids) == 0 {
			msg.err = errors.New("no hay módulos seleccionados; pulsa r y marca al menos uno con Espacio")
			return msg
		}
		status, err := engine.ReadAccount()
		if err != nil {
			msg.err = fmt.Errorf("cuenta ChatGPT no verificada: %w; ejecuta codex login", err)
			return msg
		}
		msg.account = status
		msg.dependencies, msg.err = engine.PlanDependencies(ids)
		if msg.err != nil || msg.dependencies.NeedsInstall() {
			return msg
		}
		msg.plan, msg.err = engine.BuildPlanWithAccount(ids, choices, status)
		return msg
	}
}

// Bubble Tea releases the terminal so sudo can read its own password prompt.
// The dependency plan stays in memory; no shell, serialized command or credentials.
type dependencyCommand struct {
	engine         backend
	plan           *installer.DependencyPlan
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (c *dependencyCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *dependencyCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *dependencyCommand) SetStderr(w io.Writer) { c.stderr = w }
func (c *dependencyCommand) Run() error {
	return c.engine.InstallDependencies(c.plan, c.stdin, c.stdout, c.stderr)
}
