// Package ui provides the interactive, explicitly confirmed installer.
package ui

import (
	"errors"
	"fmt"
	"io"
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

// backend keeps terminal interaction independently testable. Dependency installs
// and configuration writes have separate confirmations.
type backend interface {
	Resolve([]string) ([]installer.Module, error)
	ReadAccount() (*installer.AccountStatus, error)
	PlanDependencies([]string) (*installer.DependencyPlan, error)
	InstallDependencies(*installer.DependencyPlan, io.Reader, io.Writer, io.Writer) error
	BuildPlanWithAccount([]string, map[string]installer.ModelChoice, *installer.AccountStatus) (*installer.Plan, error)
	Apply(*installer.Plan, func(string)) (installer.Result, error)
	VerifyInstallation([]string) (*installer.Readiness, error)
}

type stage uint8

const (
	selection stage = iota
	preview
	installing
	finished
	modelSetup
)

type planMsg struct {
	generation   int
	plan         *installer.Plan
	err          error
	dependencies *installer.DependencyPlan
	account      *installer.AccountStatus
}

type progressMsg string
type resultMsg struct {
	result    installer.Result
	err       error
	readiness *installer.Readiness
	checkErr  error
}

type model struct {
	engine         backend
	modules        []installer.Module
	codexHome      string
	selected       map[string]bool // Explicit choices; Resolve supplies dependencies.
	resolved       map[string]bool
	resolveErr     error
	stage          stage
	cursor, offset int
	width, height  int
	dark           bool
	notice         string
	plan           *installer.Plan
	planErr        error
	building       bool
	generation     int
	events         chan tea.Msg
	logs           []string
	follow         bool
	result         installer.Result
	installErr     error
	choices        map[string]installer.ModelChoice
	modelOptions   []installer.ModelOption
	roleCursor     int
	requested      map[string]installer.ModelChoice
	accountErr     error
	dependencies   *installer.DependencyPlan
	readiness      *installer.Readiness
	checkErr       error
	verifying      bool
}

// Run starts an interactive installation. Cancelling before confirmation is a
// successful no-op. An Apply error is returned after the result is dismissed.
func Run(engine *installer.Engine) error {
	if engine == nil {
		return errors.New("no se recibió un motor de instalación")
	}
	m := newModel(engine, engine.Modules, engine.CodexHome)
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
		if m.installErr == nil && m.stage == finished && !m.readiness.Ready() {
			return errors.Join(err, m.checkErr, errors.New("archivos instalados; quedan comprobaciones pendientes"))
		}
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

func newModel(engine backend, modules []installer.Module, codexHome string) *model {
	m := &model{
		engine: engine, modules: modules, codexHome: codexHome,
		selected: make(map[string]bool), width: 80, height: 24, dark: true,
	}
	m.choices = installer.DefaultModelChoices()
	m.requested = make(map[string]installer.ModelChoice)
	for _, module := range modules {
		if module.Default {
			m.selected[module.ID] = true
		}
	}
	m.resolve()
	return m
}

func (m *model) Init() tea.Cmd { return tea.Batch(tea.RequestBackgroundColor, m.accountCommand()) }

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
		m.dependencies = msg.dependencies
		m.setAccount(msg.account, nil)
		if msg.plan != nil {
			for role, choice := range msg.plan.Models {
				m.choices[role] = choice
			}
		}
		if m.planErr == nil && m.plan == nil && !m.dependencies.NeedsInstall() {
			m.planErr = errors.New("el motor no devolvió un plan; vuelve a la selección e inténtalo de nuevo")
		}
	case accountMsg:
		m.setAccount(msg.status, msg.err)
	case dependencyResultMsg:
		m.stage, m.offset = preview, 0
		if msg.err != nil {
			m.building, m.planErr = false, msg.err
			return m, nil
		}
		return m, m.preparePlan()
	case verificationMsg:
		if m.stage == finished {
			m.verifying, m.readiness, m.checkErr = false, msg.readiness, msg.err
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
			m.readiness, m.checkErr = msg.readiness, msg.checkErr
			m.offset, m.follow = 0, false
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := key.String()
	if m.stage == modelSetup {
		switch k {
		case "esc", "enter":
			m.stage, m.offset = selection, 0
			return m, nil
		case "up", "k":
			m.roleCursor = max(0, m.roleCursor-1)
		case "down", "j":
			m.roleCursor = min(len(installer.ModelRoles)-1, m.roleCursor+1)
		case "d":
			m.choices = installer.DefaultModelChoices()
			clear(m.requested)
		case "left", "right", "tab", "shift+tab":
			role := installer.ModelRoles[m.roleCursor].ID
			if len(m.modelOptions) == 0 {
				m.notice = "Catálogo de la cuenta no disponible; vuelve y pulsa c para comprobar la sesión ChatGPT."
				return m, nil
			}
			m.notice = ""
			choice := m.choices[role]
			index := 0
			for i, option := range m.modelOptions {
				if option.Model == choice.Model {
					index = i
					break
				}
			}
			if k == "left" || k == "right" {
				delta := 1
				if k == "left" {
					delta = -1
				}
				index = (index + delta + len(m.modelOptions)) % len(m.modelOptions)
				choice.Model = m.modelOptions[index].Model
				choice.Effort = m.modelOptions[index].Efforts[0]
				for _, effort := range m.modelOptions[index].Efforts {
					if effort == "medium" {
						choice.Effort = effort
					}
				}
			} else {
				efforts := m.modelOptions[index].Efforts
				ei := 0
				for i, e := range efforts {
					if e == choice.Effort {
						ei = i
						break
					}
				}
				delta := 1
				if k == "shift+tab" {
					delta = -1
				}
				choice.Effort = efforts[(ei+delta+len(efforts))%len(efforts)]
			}
			m.choices[role] = choice
			m.requested[role] = choice
		}
		if k != "q" && k != "ctrl+c" {
			m.ensureCursor()
			return m, nil
		}
	}
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
	if m.stage == finished && k == "r" && !m.verifying && m.installErr == nil {
		m.verifying = true
		engine, ids := m.engine, m.ids()
		return m, func() tea.Msg { r, err := engine.VerifyInstallation(ids); return verificationMsg{r, err} }
	}
	if m.stage == selection {
		switch k {
		case "c":
			return m, m.accountCommand()
		case "m":
			m.stage, m.offset, m.roleCursor = modelSetup, 0, 0
			return m, nil
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
			return m, m.preparePlan()
		}
	} else if m.stage == preview {
		switch k {
		case "r", "backspace":
			m.stage, m.offset, m.building = selection, 0, false
			m.ensureCursor()
		case "enter":
			if !key.IsRepeat && !m.building && m.planErr == nil && m.dependencies.NeedsInstall() {
				m.stage = installing
				command := &dependencyCommand{engine: m.engine, plan: m.dependencies}
				return m, tea.Exec(command, func(err error) tea.Msg { return dependencyResultMsg{err} })
			}
			if !key.IsRepeat && !m.building && m.planErr == nil && m.plan != nil {
				m.stage, m.offset, m.follow = installing, 0, true
				m.notice = ""
				m.events = make(chan tea.Msg, 128)
				engine, plan, events, ids := m.engine, m.plan, m.events, m.ids()
				// Both commands run outside Update. Progress never blocks Apply;
				// a burst may skip log lines, but never the final result.
				return m, tea.Batch(func() tea.Msg {
					result, err := engine.Apply(plan, func(line string) {
						select {
						case events <- progressMsg(line):
						default:
						}
					})
					msg := resultMsg{result: result, err: err}
					if err == nil {
						events <- progressMsg("Verificando archivos, dependencias, cuenta y confianza de hooks…")
						msg.readiness, msg.checkErr = engine.VerifyInstallation(ids)
					}
					events <- msg
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
	if m.stage != selection && m.stage != modelSetup {
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
	switch m.stage {
	case modelSetup:
		add("Modelos · suscripción ChatGPT", accent)
		add("Preset orientativo: Astra · Sol · Spark · Luna. La revisión ajusta los valores no elegidos a tu cuenta.", muted)
		if len(m.modelOptions) == 0 {
			add("Catálogo de cuenta sin verificar; no se pueden elegir modelos todavía.", warning)
		}
		add("", plain)
		for i, role := range installer.ModelRoles {
			start := len(lines)
			style := plain
			marker := "  "
			if i == m.roleCursor {
				style = accent
				marker = "› "
			}
			choice := m.choices[role.ID]
			add(marker+role.Label+": "+choice.Model+" / "+choice.Effort, style)
			if i == m.roleCursor {
				focusStart, focusEnd = start, len(lines)-1
			}
		}
	case selection:
		if m.accountErr != nil {
			add("Cuenta: "+m.accountErr.Error()+" · codex login y c para comprobar", warning)
		}
		if len(m.modules) == 0 {
			add("No hay módulos disponibles.", warning)
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
		}
		if len(m.modules) > 0 {
			module := m.modules[m.cursor]
			add("", plain)
			add(module.Description, muted)
			if len(module.Depends) > 0 {
				add("Requiere: "+strings.Join(module.Depends, ", "), muted)
			}
		}
		if m.resolveErr != nil {
			add(m.resolveErr.Error(), warning)
		}
	case preview:
		add("Revisión · configuración sin cambios aún", accent)
		add("Destino: "+m.codexHome, muted)
		add("", plain)
		if m.building {
			add("Preparando…", muted)
		} else if m.planErr != nil {
			add("No se puede instalar: "+m.planErr.Error(), warning)
		} else if m.dependencies.NeedsInstall() {
			add("Primero: instalar dependencias", accent)
			add(strings.Join(m.dependencies.Missing, ", "), plain)
			for _, command := range m.dependencies.Commands {
				add(command.Path+" "+strings.Join(command.Args, " "), plain)
			}
			for _, text := range m.dependencies.Warnings {
				add(text, warning)
			}
			add("Enter autoriza estos paquetes y descargas. sudo puede pedir tu contraseña en la terminal. Después revisarás y confirmarás la configuración por separado.", muted)
		} else if m.plan != nil {
			for _, role := range installer.ModelRoles {
				choice, ok := m.plan.Models[role.ID]
				if !ok {
					continue
				}
				add(role.Label+": "+choice.Model+" / "+choice.Effort, muted)
			}
			names := make([]string, 0, len(m.plan.Modules))
			for _, module := range m.plan.Modules {
				names = append(names, module.Name)
			}
			add("Módulos: "+strings.Join(names, ", "), plain)
			for _, text := range m.plan.Warnings {
				add("⚠ "+text, warning)
			}
			add(fmt.Sprintf("Cambios (%d):", len(m.plan.Changes)), accent)
			for _, change := range m.plan.Changes {
				add("  ["+change.Kind+"] "+change.Path, plain)
			}
			if len(m.plan.Changes) == 0 {
				add("  Ninguno", muted)
			}
		}
	case installing:
		if len(m.logs) == 0 {
			add("Iniciando…", muted)
		}
		for _, line := range m.logs {
			add(line, plain)
		}
	case finished:
		if m.installErr != nil {
			add("Instalación fallida", warning)
			add(m.installErr.Error(), warning)
		} else {
			if m.readiness.Ready() && m.checkErr == nil {
				add("✓ Listo para usar: selección verificada", accent)
			} else {
				add("Archivos instalados · verificación pendiente", warning)
			}
		}
		add(fmt.Sprintf("Archivos cambiados: %d", m.result.Changed), plain)
		if m.result.BackupDir != "" {
			add("Respaldo: "+m.result.BackupDir, plain)
		}
		if m.verifying {
			add("Comprobando…", muted)
		}
		if m.checkErr != nil {
			add(m.checkErr.Error(), warning)
		}
		if m.readiness != nil {
			for _, pending := range m.readiness.Pending {
				add("Pendiente: "+pending, warning)
			}
		}
		if m.installErr == nil && !m.readiness.Ready() {
			add("Abre Codex con este CODEX_HOME, revisa /hooks y concede confianza sólo a los hooks que aceptes. Vuelve aquí y pulsa r; no se concede confianza automáticamente.", muted)
		}
		if m.installErr != nil && len(m.logs) > 0 {
			add("", plain)
			add("Últimos pasos:", muted)
			for _, line := range m.logs {
				add(line, plain)
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
	title := []string{"1/3  Módulos", "2/3  Revisión", "3/3  Instalación", "Resultado", "Modelos"}[m.stage]
	foot := "↑/↓ mover · espacio marcar · m modelos · c cuenta · a todos · n ninguno · enter revisar · q salir"
	switch m.stage {
	case modelSetup:
		foot = "↑/↓ rol · ←/→ modelo · tab esfuerzo · d preset · enter volver"
	case preview:
		foot = "enter instalar · r editar · q cancelar"
		if m.dependencies.NeedsInstall() {
			foot = "enter autorizar dependencias · r editar · q cancelar"
		}
		if m.building || m.planErr != nil {
			foot = "r editar · q cancelar"
		}
	case installing:
		foot = "instalando · salida bloqueada"
	case finished:
		foot = "r comprobar de nuevo · enter/q cerrar"
	}
	if len(lines) > capacity {
		foot = fmt.Sprintf("↕ %d–%d/%d · PgUp/PgDn · ", offset+1, end, len(lines)) + foot
	}
	if m.height >= 3 {
		body = append([]string{accent.Render("Codex Setup  ·  " + title)}, body...)
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
