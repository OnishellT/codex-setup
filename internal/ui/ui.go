// Package ui provides the interactive, explicitly confirmed installer.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"codex-setup/internal/installer"
)

// backend keeps terminal interaction independently testable. Only Apply writes.
type backend interface {
	Resolve([]string) ([]installer.Module, error)
	BuildPlan([]string) (*installer.Plan, error)
	Apply(*installer.Plan, func(string)) (installer.Result, error)
}

type stage uint8

const (
	selection stage = iota
	preview
	installing
	finished
)

type planMsg struct {
	generation int
	plan       *installer.Plan
	err        error
}

type progressMsg string
type resultMsg struct {
	result installer.Result
	err    error
}

type model struct {
	engine          backend
	modules         []installer.Module
	home, codexHome string
	selected        map[string]bool // Explicit choices; Resolve supplies dependencies.
	resolved        map[string]bool
	resolveErr      error
	stage           stage
	cursor, offset  int
	width, height   int
	dark            bool
	notice          string
	plan            *installer.Plan
	planErr         error
	building        bool
	generation      int
	events          chan tea.Msg
	logs            []string
	follow          bool
	result          installer.Result
	installErr      error
}

// Run starts an interactive installation. Cancelling before confirmation is a
// successful no-op. An Apply error is returned after the result is dismissed.
func Run(engine *installer.Engine) error {
	if engine == nil {
		return errors.New("no se recibió un motor de instalación")
	}
	m := newModel(engine, engine.Modules, engine.Home, engine.CodexHome)
	p := tea.NewProgram(m, tea.WithFilter(transactionFilter), tea.WithoutSignalHandler())
	// Bubble Tea's default signal listener stops after the first signal, even
	// when a filter rejects it. Keep listening until Run ends so a second
	// SIGINT/SIGTERM cannot interrupt an active transaction either.
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-signals:
				p.Send(tea.QuitMsg{})
			}
		}
	}()
	final, err := p.Run()
	return runError(final, err)
}

func runError(final tea.Model, err error) error {
	if m, ok := final.(*model); ok {
		return errors.Join(err, m.installErr)
	}
	return err
}

// Bubble Tea also routes SIGINT/SIGTERM through these messages. Do not let
// keyboard shortcuts or normal shutdown signals interrupt a transaction.
func transactionFilter(current tea.Model, msg tea.Msg) tea.Msg {
	if m, ok := current.(*model); ok && m.stage == installing {
		switch msg.(type) {
		case tea.QuitMsg, tea.InterruptMsg, tea.SuspendMsg:
			return nil
		}
	}
	return msg
}

func newModel(engine backend, modules []installer.Module, home, codexHome string) *model {
	m := &model{
		engine: engine, modules: modules, home: home, codexHome: codexHome,
		selected: make(map[string]bool), width: 80, height: 24, dark: true,
	}
	for _, module := range modules {
		if module.Default {
			m.selected[module.ID] = true
		}
	}
	m.resolve()
	return m
}

func (m *model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *model) ids() []string {
	ids := make([]string, 0, len(m.selected))
	for _, module := range m.modules {
		if m.selected[module.ID] {
			ids = append(ids, module.ID)
		}
	}
	return ids
}

func (m *model) resolve() {
	m.resolved = make(map[string]bool)
	modules, err := m.engine.Resolve(m.ids())
	m.resolveErr = err
	for _, module := range modules {
		m.resolved[module.ID] = true
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.ensureCursor()
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case planMsg:
		if m.stage != preview || msg.generation != m.generation {
			return m, nil
		}
		m.building, m.plan, m.planErr = false, msg.plan, msg.err
		if m.planErr == nil && m.plan == nil {
			m.planErr = errors.New("el motor no devolvió un plan; vuelve a la selección e inténtalo de nuevo")
		}
	case progressMsg:
		if m.stage == installing {
			m.logs = append(m.logs, string(msg))
			if len(m.logs) > 500 {
				m.logs = m.logs[len(m.logs)-500:]
			}
			return m, waitEvent(m.events)
		}
	case resultMsg:
		if m.stage == installing {
			m.stage, m.result, m.installErr = finished, msg.result, msg.err
			m.offset, m.follow = 0, false
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := key.String()
	if k == "q" || k == "esc" || k == "ctrl+c" {
		if m.stage == installing {
			m.notice = "Transacción en curso: espera al resultado para salir."
			return m, nil
		}
		return m, tea.Quit
	}
	if m.stage == finished && k == "enter" {
		return m, tea.Quit
	}
	if m.stage == selection {
		switch k {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
			m.ensureCursor()
		case "down", "j":
			m.cursor = min(max(0, len(m.modules)-1), m.cursor+1)
			m.ensureCursor()
		case "home":
			m.cursor, m.offset = 0, 0
		case "end":
			m.cursor = max(0, len(m.modules)-1)
			m.ensureCursor()
		case "space", " ":
			if len(m.modules) > 0 {
				id := m.modules[m.cursor].ID
				m.notice = ""
				if m.resolved[id] && !m.selected[id] {
					m.notice = "Dependencia necesaria: desmarca primero los módulos que la requieren."
				} else {
					m.selected[id] = !m.selected[id]
					m.resolve()
				}
			}
		case "a", "n":
			for _, module := range m.modules {
				m.selected[module.ID] = k == "a"
			}
			m.notice = ""
			m.resolve()
		case "enter":
			if key.IsRepeat {
				return m, nil
			}
			m.stage, m.building, m.offset = preview, true, 0
			m.plan, m.planErr, m.notice = nil, nil, ""
			m.generation++
			ids, generation, engine := m.ids(), m.generation, m.engine
			return m, func() tea.Msg {
				if len(ids) == 0 {
					return planMsg{generation: generation, err: errors.New("no hay módulos seleccionados; pulsa r y marca al menos uno con Espacio")}
				}
				plan, err := engine.BuildPlan(ids)
				return planMsg{generation: generation, plan: plan, err: err}
			}
		}
	} else if m.stage == preview {
		switch k {
		case "r", "backspace":
			m.stage, m.offset, m.building = selection, 0, false
			m.ensureCursor()
		case "enter":
			if !key.IsRepeat && !m.building && m.planErr == nil && m.plan != nil {
				m.stage, m.offset, m.follow = installing, 0, true
				m.notice = ""
				m.events = make(chan tea.Msg, 128)
				engine, plan, events := m.engine, m.plan, m.events
				// Both commands run outside Update. Progress never blocks Apply;
				// a burst may skip log lines, but never the final result.
				return m, tea.Batch(func() tea.Msg {
					result, err := engine.Apply(plan, func(line string) {
						select {
						case events <- progressMsg(line):
						default:
						}
					})
					events <- resultMsg{result: result, err: err}
					return nil
				}, waitEvent(events))
			}
		}
	}
	// Page scrolling works in every stage, including long module descriptions.
	delta := 0
	switch k {
	case "pgdown", "ctrl+d":
		delta = m.bodyHeight()
	case "pgup", "ctrl+u":
		delta = -m.bodyHeight()
	case "up", "k":
		if m.stage != selection {
			delta = -1
		}
	case "down", "j":
		if m.stage != selection {
			delta = 1
		}
	case "home":
		if m.stage != selection {
			m.offset, m.follow = 0, false
		}
	case "end":
		if m.stage != selection {
			lines, _, _ := m.document()
			m.offset = max(0, len(lines)-m.bodyHeight())
			m.follow = m.stage == installing
		}
	}
	if delta != 0 {
		lines, _, _ := m.document()
		if m.stage == installing && m.follow {
			m.offset = max(0, len(lines)-m.bodyHeight())
		}
		m.offset = min(max(0, len(lines)-m.bodyHeight()), max(0, m.offset+delta))
		m.follow = false
	}
	return m, nil
}

func waitEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-events }
}

// Reserve a title and footer when possible; even a one-row terminal can scroll.
func (m *model) bodyHeight() int { return max(1, m.height-2) }

func (m *model) ensureCursor() {
	if m.stage != selection {
		return
	}
	_, start, end := m.document()
	if start < m.offset {
		m.offset = start
	} else if end >= m.offset+m.bodyHeight() {
		m.offset = max(start, end-m.bodyHeight()+1)
	}
}

// Treat paths, backend messages and descriptions as text, never terminal codes.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

func (m *model) styles() (lipgloss.Style, lipgloss.Style, lipgloss.Style) {
	color := lipgloss.LightDark(m.dark)
	return lipgloss.NewStyle().Bold(true).Foreground(color(lipgloss.Color("#5B21B6"), lipgloss.Color("#C4B5FD"))),
		lipgloss.NewStyle().Foreground(color(lipgloss.Color("#475569"), lipgloss.Color("#94A3B8"))),
		lipgloss.NewStyle().Foreground(color(lipgloss.Color("#9A3412"), lipgloss.Color("#FDBA74")))
}

func (m *model) document() (lines []string, focusStart, focusEnd int) {
	accent, muted, warning := m.styles()
	add := func(text string, style lipgloss.Style) {
		wrapped := strings.Split(ansi.Wrap(clean(text), max(1, m.width), " /"), "\n")
		for _, line := range wrapped {
			lines = append(lines, style.Render(line))
		}
	}
	plain := lipgloss.NewStyle()
	add("Home destino: "+m.home, muted)
	add("Codex home: "+m.codexHome, muted)
	add("", plain)
	switch m.stage {
	case selection:
		add("Elige módulos · las dependencias se añaden automáticamente", muted)
		add("Espacio: marcar · a: todos · n: ninguno · Enter: revisar", muted)
		if len(m.modules) == 0 {
			add("No hay módulos disponibles. Pulsa q para salir.", warning)
		}
		for i, module := range m.modules {
			start := len(lines)
			marker, check, suffix := "  ", "[ ]", ""
			style := plain
			if m.selected[module.ID] || m.resolved[module.ID] {
				check = "[x]"
			}
			if m.resolved[module.ID] && !m.selected[module.ID] {
				suffix = " · dependencia"
			}
			if i == m.cursor {
				marker, style = "› ", accent
			}
			add(marker+check+" "+module.Name+suffix, style)
			if i == m.cursor {
				focusStart, focusEnd = start, len(lines)-1
			}
			add("    "+module.Description, muted)
			if len(module.Depends) > 0 {
				add("    Requiere: "+strings.Join(module.Depends, ", "), muted)
			}
			if len(module.Platforms) > 0 {
				add("    Plataformas: "+strings.Join(module.Platforms, ", "), muted)
			}
		}
		if m.resolveErr != nil {
			add("Error de selección: "+m.resolveErr.Error(), warning)
			add("Revisa los módulos y sus dependencias antes de continuar.", warning)
		}
	case preview:
		add("VISTA PREVIA · solo lectura; todavía no se ha instalado nada", accent)
		if m.building {
			add("Comprobando dependencias y preparando el plan…", muted)
		} else if m.planErr != nil {
			add("No se puede instalar: "+m.planErr.Error(), warning)
			add("Corrige el problema indicado. Pulsa r para cambiar la selección y volver a comprobar; q cancela.", warning)
		} else if m.plan != nil {
			add("Módulos incluidos:", accent)
			for _, module := range m.plan.Modules {
				add("  • "+module.Name+" ("+module.ID+")", plain)
			}
			for _, text := range m.plan.Warnings {
				add("Advertencia: "+text, warning)
			}
			add(fmt.Sprintf("Archivos destino (%d):", len(m.plan.Changes)), accent)
			for _, change := range m.plan.Changes {
				add("  ["+change.Kind+"] "+change.Path, plain)
			}
			if len(m.plan.Changes) == 0 {
				add("Sin cambios de archivos previstos.", muted)
			}
			add("CONFIRMACIÓN: pulsa Enter para instalar este plan; r permite editar y q cancela.", accent)
		}
	case installing:
		add("INSTALANDO · no cierres la terminal", accent)
		add("La transacción debe terminar antes de salir. ↑/↓ y PgUp/PgDn: registro.", warning)
		if len(m.logs) == 0 {
			add("Iniciando instalación…", muted)
		}
		for _, line := range m.logs {
			add("  "+line, plain)
		}
	case finished:
		if m.installErr != nil {
			add("INSTALACIÓN FALLIDA", warning)
			add(m.installErr.Error(), warning)
			add("Revisa el error y el respaldo, si existe, antes de volver a intentarlo.", muted)
		} else {
			add("INSTALACIÓN COMPLETADA", accent)
		}
		add(fmt.Sprintf("Archivos cambiados: %d", m.result.Changed), plain)
		if m.result.BackupDir != "" {
			add("Respaldo: "+m.result.BackupDir, plain)
		}
		add("Enter o q: cerrar", muted)
		if len(m.logs) > 0 {
			add("Registro reciente:", accent)
			for _, line := range m.logs {
				add("  "+line, plain)
			}
		}
	}
	if m.notice != "" {
		add(m.notice, warning)
	}
	return lines, focusStart, focusEnd
}

func (m *model) View() tea.View {
	accent, muted, _ := m.styles()
	lines, _, _ := m.document()
	capacity := m.bodyHeight()
	offset := min(m.offset, max(0, len(lines)-capacity))
	if m.stage == installing && m.follow {
		offset = max(0, len(lines)-capacity)
	}
	end := min(len(lines), offset+capacity)
	body := append([]string(nil), lines[offset:end]...)
	for len(body) < capacity {
		body = append(body, "")
	}
	title := []string{"1/3 · Módulos", "2/3 · Revisión", "3/3 · Instalación", "Resultado"}[m.stage]
	foot := "Enter: revisar · Espacio: marcar · q/esc: cancelar"
	switch m.stage {
	case preview:
		foot = "Enter: instalar · r: editar · q/esc: cancelar"
		if m.building || m.planErr != nil {
			foot = "r: editar · q/esc: cancelar"
		}
	case installing:
		foot = "Espera al resultado · salida bloqueada"
	case finished:
		foot = "Enter/q: cerrar"
	}
	if len(lines) > capacity {
		foot = fmt.Sprintf("↕ %d–%d/%d · PgUp/PgDn · ", offset+1, end, len(lines)) + foot
	}
	if m.height >= 3 {
		body = append([]string{accent.Render("CODEX SETUP · " + title)}, body...)
		body = append(body, muted.Render(foot))
	} else if m.height == 2 {
		body = append(body, muted.Render(foot))
	}
	for i := range body {
		body[i] = ansi.Truncate(body[i], max(1, m.width), "")
	}
	v := tea.NewView(strings.Join(body, "\n"))
	v.AltScreen = true
	return v
}
