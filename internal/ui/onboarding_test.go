package ui

import (
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"codex-setup/internal/installer"
)

func TestDependenciesNeedSeparateConfirmation(t *testing.T) {
	m, f := fixture()
	f.dependencies = &installer.DependencyPlan{Missing: []string{"python3"}, Commands: []installer.DependencyCommand{{Path: "/usr/bin/sudo", Args: []string{"/usr/bin/apt-get", "install", "python3"}}}}
	prepare(t, m)
	if f.buildCalls != 0 || f.dependencyCalls != 0 || f.applyCalls != 0 {
		t.Fatal("dependency preview wrote or planned config")
	}
	if !strings.Contains(m.View().Content, "sudo") {
		t.Fatal("missing dependency command preview")
	}
	confirmed := m.dependencies
	if press(m, tea.KeyEnter) == nil || m.stage != installing || f.dependencyCalls != 0 {
		t.Fatal("confirmation did not schedule terminal operation")
	}
	in := strings.NewReader("input")
	out := &strings.Builder{}
	f.dependencyInstall = func(p *installer.DependencyPlan, r io.Reader, w, stderr io.Writer) error {
		if p != confirmed || r != in || w != out || stderr != out {
			t.Fatal("plan or terminal descriptors changed")
		}
		f.dependencies = nil
		return nil
	}
	command := &dependencyCommand{engine: f, plan: confirmed}
	command.SetStdin(in)
	command.SetStdout(out)
	command.SetStderr(out)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	_, next := m.Update(dependencyResultMsg{})
	if next == nil {
		t.Fatal("did not recheck after dependency install")
	}
	m.Update(next())
	if m.stage != preview || m.plan == nil || f.applyCalls != 0 {
		t.Fatal("dependency success applied configuration without second consent")
	}
	if press(m, tea.KeyEnter) == nil || m.stage != installing {
		t.Fatal("configuration confirmation unavailable")
	}
}

func TestDependencyFailureAndAccountFailureNeverApply(t *testing.T) {
	m, f := fixture()
	f.accountErr = errors.New("not logged in")
	cmd := press(m, tea.KeyEnter)
	m.Update(cmd())
	if m.planErr == nil || f.buildCalls != 0 || f.dependencyCalls != 0 {
		t.Fatal("unverified account continued")
	}
	if press(m, tea.KeyEnter) != nil {
		t.Fatal("unverified preview confirmed")
	}
	m.stage = installing
	m.Update(dependencyResultMsg{errors.New("sudo cancelled")})
	if m.stage != preview || m.planErr == nil || f.applyCalls != 0 {
		t.Fatal("dependency failure continued")
	}
}

func TestReadinessPendingCanRecheckWithoutInstall(t *testing.T) {
	m, f := fixture()
	m.stage = finished
	m.readiness = &installer.Readiness{Pending: []string{"revisa /hooks"}}
	if strings.Contains(m.View().Content, "Listo para usar") || runError(m, nil) == nil {
		t.Fatal("pending trust reported ready")
	}
	cmd := press(m, 'r')
	if cmd == nil || !m.verifying {
		t.Fatal("recheck not scheduled")
	}
	m.Update(cmd())
	if !m.readiness.Ready() || f.applyCalls != 0 || f.dependencyCalls != 0 || f.verifyCalls != 1 {
		t.Fatal("recheck wrote or did not verify")
	}
	if runError(m, nil) != nil {
		t.Fatal("verified install returned failure")
	}
}

func TestModelsUseLiveCatalogAndTrackOnlyExplicitChoices(t *testing.T) {
	m, _ := fixture()
	m.setAccount(&installer.AccountStatus{Models: []installer.ModelOption{{Model: "live-only", Efforts: []string{"high"}}}}, nil)
	press(m, 'm')
	press(m, tea.KeyRight)
	if m.choices["principal"].Model != "live-only" || len(m.requested) != 1 {
		t.Fatal("model choices not from live account")
	}
	press(m, 'd')
	if len(m.requested) != 0 {
		t.Fatal("preset counted as explicit user choices")
	}
	m.setAccount(nil, errors.New("offline"))
	before := m.choices["principal"]
	press(m, tea.KeyRight)
	if m.choices["principal"] != before {
		t.Fatal("offline catalog allowed selection")
	}
}
