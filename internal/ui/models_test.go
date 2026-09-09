package ui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"codex-setup/internal/installer"
)

func TestModelScreenEditsAndRestoresPreset(t *testing.T) {
	m, _ := fixture()
	m.key(tea.KeyPressMsg{Code: 'm'})
	if m.stage != modelSetup {
		t.Fatal("model screen not opened")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	before := m.choices["default"]
	m.key(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.choices["default"].Model == before.Model {
		t.Fatal("model not changed")
	}
	before = m.choices["default"]
	m.key(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.choices["default"].Effort == before.Effort {
		t.Fatal("effort not changed")
	}
	m.key(tea.KeyPressMsg{Code: 'd'})
	if !reflect.DeepEqual(m.choices, installer.DefaultModelChoices()) {
		t.Fatal("preset not restored")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.stage != selection {
		t.Fatal("model screen did not return to modules")
	}
}
