package ui

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"codex-setup/internal/installer"
)

type fakeBackend struct {
	modules    []installer.Module
	plan       *installer.Plan
	planErr    error
	buildCalls int
	applyCalls int
	buildIDs   []string
	applied    *installer.Plan
	apply      func(*installer.Plan, func(string)) (installer.Result, error)
}

func (f *fakeBackend) Resolve(ids []string) ([]installer.Module, error) {
	// Exercise the real dependency resolver without filesystem operations.
	return (&installer.Engine{Modules: f.modules}).Resolve(ids)
}

func (f *fakeBackend) BuildPlan(ids []string) (*installer.Plan, error) {
	f.buildCalls++
	f.buildIDs = append([]string(nil), ids...)
	return f.plan, f.planErr
}

func (f *fakeBackend) Apply(plan *installer.Plan, progress func(string)) (installer.Result, error) {
	f.applyCalls++
	f.applied = plan
	if f.apply != nil {
		return f.apply(plan, progress)
	}
	progress("Escribiendo configuración")
	return installer.Result{Changed: 1, BackupDir: "/destino/respaldo"}, nil
}

func fixture() (*model, *fakeBackend) {
	modules := []installer.Module{
		{ID: "base", Name: "Configuración base", Description: "Opciones compartidas"},
		{ID: "tools", Name: "Herramientas", Description: "Herramientas para desarrollo", Depends: []string{"base"}, Default: true},
		{ID: "extra", Name: "Extras", Description: "Complementos opcionales"},
	}
	f := &fakeBackend{modules: modules, plan: &installer.Plan{
		Modules:  modules[:2],
		Changes:  []installer.Change{{Path: "/destino/.codex/config.toml", Kind: "actualizar"}},
		Warnings: []string{"Se eliminarán comentarios TOML."},
	}}
	return newModel(f, modules, "/destino", "/destino/.codex"), f
}

func press(m *model, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func requireQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected QuitMsg")
	}
}

func prepare(t *testing.T, m *model) {
	t.Helper()
	cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("missing asynchronous BuildPlan command")
	}
	m.Update(cmd())
	if m.stage != preview || m.building || m.planErr != nil {
		t.Fatalf("preview not ready: stage=%v building=%v err=%v", m.stage, m.building, m.planErr)
	}
}

func TestNavigationAndDependencies(t *testing.T) {
	m, f := fixture()
	if !m.selected["tools"] || m.selected["base"] || !m.resolved["base"] {
		t.Fatal("default selection must include the implicit base dependency")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "dependencia") {
		t.Fatal("implicit dependency is not labelled")
	}
	press(m, tea.KeySpace)
	if !m.resolved["base"] || m.selected["base"] || m.notice == "" {
		t.Fatal("a required dependency must remain checked with an explanation")
	}
	press(m, tea.KeyUp)
	if m.cursor != 0 {
		t.Fatal("cursor crossed the start")
	}
	press(m, tea.KeyDown)
	press(m, tea.KeySpace)
	if m.selected["tools"] || m.resolved["base"] {
		t.Fatal("removing a dependent must remove its unused implicit dependency")
	}
	press(m, 'j')
	press(m, 'j')
	if m.cursor != 2 {
		t.Fatal("cursor crossed the end")
	}
	press(m, 'k')
	if m.cursor != 1 {
		t.Fatal("vim navigation failed")
	}
	press(m, 'a')
	if len(m.ids()) != 3 || len(m.resolved) != 3 {
		t.Fatal("select-all failed")
	}
	press(m, 'n')
	if len(m.ids()) != 0 || len(m.resolved) != 0 {
		t.Fatal("deselect-all failed")
	}
	if f.buildCalls != 0 || f.applyCalls != 0 {
		t.Fatal("selection performed planning or installation")
	}
}

func TestFirstEnterOnlyPreviewsSecondConfirms(t *testing.T) {
	m, f := fixture()
	cmd := press(m, tea.KeyEnter)
	if m.stage != preview || !m.building || cmd == nil || f.buildCalls != 0 || f.applyCalls != 0 {
		t.Fatal("first Enter must schedule a read-only preview, not do synchronous work")
	}
	if press(m, tea.KeyEnter) != nil {
		t.Fatal("Enter while planning must not confirm")
	}
	m.Update(cmd())
	if f.buildCalls != 1 || f.applyCalls != 0 || !reflect.DeepEqual(f.buildIDs, []string{"tools"}) {
		t.Fatal("preview did not build exactly the explicit selection")
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"solo lectura", "Configuración base", "/destino/.codex/config.toml", "Advertencia:", "CONFIRMACIÓN", "Home destino: /destino", "Codex home: /destino/.codex"} {
		if !strings.Contains(view, want) {
			t.Errorf("preview missing %q:\n%s", want, view)
		}
	}
	if _, repeat := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, IsRepeat: true}); repeat != nil || m.stage != preview {
		t.Fatal("held Enter must not confirm")
	}
	cmd = press(m, tea.KeyEnter)
	if cmd == nil || m.stage != installing || f.applyCalls != 0 {
		t.Fatal("confirmation must schedule Apply asynchronously")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("expected Apply and progress listener commands")
	}
	batch[0]()
	if f.applyCalls != 1 || f.applied != f.plan {
		t.Fatal("Apply must receive the exact confirmed plan once")
	}
	_, next := m.Update(batch[1]())
	if len(m.logs) != 1 || next == nil {
		t.Fatal("progress was not displayed or listener stopped")
	}
	m.Update(next())
	if m.stage != finished || m.result.Changed != 1 {
		t.Fatal("missing final result")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/destino/respaldo") {
		t.Fatal("backup path missing from result")
	}
	requireQuit(t, press(m, tea.KeyEnter))
	if err := runError(m, nil); err != nil {
		t.Fatalf("successful install returned %v", err)
	}
}

func TestPlanErrorsAndStalePlans(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		nilPlan, empty bool
	}{
		{name: "backend", err: errors.New("TOML inválido: corrige /destino/config.toml")},
		{name: "nil plan", nilPlan: true},
		{name: "empty selection", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f := fixture()
			f.planErr = tc.err
			if tc.nilPlan {
				f.plan = nil
			}
			if tc.empty {
				press(m, 'n')
			}
			cmd := press(m, tea.KeyEnter)
			m.Update(cmd())
			if m.planErr == nil || m.building {
				t.Fatal("expected a visible planning error")
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "No se puede instalar") || !strings.Contains(view, "Pulsa r") {
				t.Fatalf("error needs actionable guidance: %s", view)
			}
			if tc.err != nil && !strings.Contains(view, tc.err.Error()) {
				t.Fatal("backend error details were lost")
			}
			if press(m, tea.KeyEnter) != nil || f.applyCalls != 0 {
				t.Fatal("invalid preview must never install")
			}
			requireQuit(t, press(m, tea.KeyEscape))
		})
	}
	m, _ := fixture()
	old := press(m, tea.KeyEnter)
	press(m, 'r')
	current := press(m, tea.KeyEnter)
	m.Update(old())
	if !m.building || m.plan != nil {
		t.Fatal("stale plan replaced current selection")
	}
	m.Update(current())
	if m.building || m.plan == nil {
		t.Fatal("current plan was ignored")
	}
}

func TestCancelBeforeConfirmation(t *testing.T) {
	for _, previewFirst := range []bool{false, true} {
		for _, key := range []tea.KeyPressMsg{{Code: 'q'}, {Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
			m, f := fixture()
			if previewFirst {
				prepare(t, m)
			}
			_, cmd := m.Update(key)
			requireQuit(t, cmd)
			if f.applyCalls != 0 {
				t.Fatal("cancel installed files")
			}
		}
	}
	// Cancelling an in-flight, read-only plan is also allowed.
	m, _ := fixture()
	press(m, tea.KeyEnter)
	requireQuit(t, press(m, 'q'))
}

func TestAsyncInstallLocksExitAndReturnsError(t *testing.T) {
	m, f := fixture()
	started, release, applied := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	wantErr := errors.New("falló la escritura; respaldo disponible")
	f.apply = func(_ *installer.Plan, progress func(string)) (installer.Result, error) {
		progress("Preparando respaldo")
		close(started)
		<-release
		return installer.Result{BackupDir: "/respaldo"}, wantErr
	}
	prepare(t, m)
	batch := press(m, tea.KeyEnter)().(tea.BatchMsg)
	go func() { batch[0](); close(applied) }()
	defer func() { unblock(); <-applied }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Apply did not start")
	}
	_, next := m.Update(batch[1]())
	if len(m.logs) != 1 || next == nil {
		t.Fatal("live progress missing")
	}
	for _, key := range []tea.KeyPressMsg{{Code: 'q'}, {Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}, {Code: tea.KeyEnter}} {
		_, cmd := m.Update(key)
		if cmd != nil || m.stage != installing {
			t.Fatal("transaction was interrupted or restarted")
		}
	}
	for _, msg := range []tea.Msg{tea.QuitMsg{}, tea.InterruptMsg{}, tea.SuspendMsg{}} {
		if transactionFilter(m, msg) != nil {
			t.Fatal("transaction allowed shutdown")
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	if m.View().Content == "" {
		t.Fatal("terminal stopped rendering during Apply")
	}
	unblock()
	m.Update(next())
	if m.stage != finished {
		t.Fatal("error did not produce a final screen")
	}
	if transactionFilter(m, tea.QuitMsg{}) == nil {
		t.Fatal("final screen cannot close")
	}
	requireQuit(t, press(m, 'q'))
	if !errors.Is(runError(m, nil), wantErr) {
		t.Fatal("Run lost the install error")
	}
	runtimeErr := errors.New("terminal failure")
	if err := runError(m, runtimeErr); !errors.Is(err, wantErr) || !errors.Is(err, runtimeErr) {
		t.Fatal("Run must preserve both installation and terminal errors")
	}
}

func TestProgressBurstDoesNotBlockApply(t *testing.T) {
	m, f := fixture()
	f.apply = func(_ *installer.Plan, progress func(string)) (installer.Result, error) {
		for i := 0; i < 2000; i++ {
			progress(fmt.Sprint(i))
		}
		return installer.Result{Changed: 2}, nil
	}
	prepare(t, m)
	batch := press(m, tea.KeyEnter)().(tea.BatchMsg)
	done := make(chan struct{})
	go func() { batch[0](); close(done) }()
	cmd := batch[1]
	for m.stage == installing {
		_, cmd = m.Update(cmd())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("progress burst blocked Apply")
	}
	if m.result.Changed != 2 || len(m.logs) > 500 {
		t.Fatal("progress/result retention failed")
	}
}

func TestNarrowRenderAndScrolling(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {2, 2}, {12, 6}, {30, 10}, {80, 24}} {
		for _, stage := range []stage{selection, preview, installing, finished} {
			t.Run(fmt.Sprintf("%dx%d/stage%d", size[0], size[1], stage), func(t *testing.T) {
				m, f := fixture()
				m.stage, m.plan = stage, f.plan
				m.home = "/un/destino/muy/largo/日本語/con/ácentos"
				for i := 0; i < 40; i++ {
					m.logs = append(m.logs, "Progreso de instalación número "+fmt.Sprint(i))
				}
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for i := 0; i < 5; i++ {
					view := m.View()
					if !view.AltScreen {
						t.Fatal("alternate screen missing")
					}
					if lipgloss.Height(view.Content) > size[1] {
						t.Fatalf("height overflow: %q", view.Content)
					}
					for _, line := range strings.Split(view.Content, "\n") {
						if lipgloss.Width(line) > size[0] {
							t.Fatalf("width overflow: %q", line)
						}
					}
					press(m, tea.KeyPgDown)
				}
			})
		}
	}
	m, _ := fixture()
	m.Update(tea.WindowSizeMsg{Width: 25, Height: 7})
	press(m, tea.KeyEnd)
	if m.offset == 0 || !strings.Contains(ansi.Strip(m.View().Content), "Extras") {
		t.Fatal("cursor did not scroll into view")
	}
	press(m, tea.KeyHome)
	if m.offset != 0 || m.cursor != 0 {
		t.Fatal("home did not reset navigation")
	}
	prepare(t, m)
	press(m, tea.KeyPgDown)
	if m.offset == 0 {
		t.Fatal("preview did not scroll")
	}
	press(m, tea.KeyHome)
	if m.offset != 0 {
		t.Fatal("preview home did not reset scroll")
	}
}

func TestAdaptiveColorsAndSafeBackendText(t *testing.T) {
	m, _ := fixture()
	if m.Init() == nil {
		t.Fatal("terminal background detection not requested")
	}
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if m.dark {
		t.Fatal("light background ignored")
	}
	light := m.View().Content
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if !m.dark || light == m.View().Content {
		t.Fatal("palette did not adapt")
	}
	if got := clean("safe\x1b[2J\x1b]0;evil\a\r\ttext"); got != "safe text" {
		t.Fatalf("terminal controls leaked: %q", got)
	}
	if Run(nil) == nil {
		t.Fatal("nil engine must return an error")
	}
}

func TestLiveProgramConfirmationAndFailure(t *testing.T) {
	m, f := fixture()
	planSeen, resultSeen := make(chan struct{}), make(chan struct{})
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	wantErr := errors.New("error de instalación de prueba")
	f.apply = func(_ *installer.Plan, progress func(string)) (installer.Result, error) {
		progress("Creando respaldo")
		close(started)
		<-release
		return installer.Result{}, wantErr
	}
	type probe chan stage
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithWindowSize(40, 12),
		tea.WithFilter(func(current tea.Model, msg tea.Msg) tea.Msg {
			switch msg := msg.(type) {
			case planMsg:
				close(planSeen)
			case resultMsg:
				close(resultSeen)
			case probe:
				msg <- current.(*model).stage
				return nil
			}
			return transactionFilter(current, msg)
		}))
	done := make(chan error, 1)
	go func() {
		final, err := p.Run()
		done <- runError(final, err)
	}()
	t.Cleanup(func() { unblock(); p.Kill() })
	await := func(ch <-chan struct{}, description string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", description)
		}
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	await(planSeen, "preview")
	state := make(probe, 1)
	p.Send(state)
	if got := <-state; got != preview {
		t.Fatalf("first Enter entered stage %v", got)
	}
	select {
	case <-started:
		t.Fatal("first Enter started installation")
	default:
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	await(started, "Apply")
	for _, msg := range []tea.Msg{tea.KeyPressMsg{Code: 'q'}, tea.KeyPressMsg{Code: tea.KeyEscape}, tea.QuitMsg{}, tea.InterruptMsg{}, tea.QuitMsg{}} {
		p.Send(msg)
	}
	p.Send(state)
	if got := <-state; got != installing {
		t.Fatalf("transaction exited into stage %v", got)
	}
	unblock()
	await(resultSeen, "final result")
	p.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("final dismissal lost Apply error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not exit after final dismissal")
	}
	if f.applyCalls != 1 || f.buildCalls != 1 {
		t.Fatalf("unexpected call counts: BuildPlan=%d Apply=%d", f.buildCalls, f.applyCalls)
	}
}

func TestProgressScrollStartsAtVisibleTail(t *testing.T) {
	m, _ := fixture()
	m.stage, m.follow = installing, true
	m.width, m.height = 30, 8
	for i := 0; i < 30; i++ {
		m.logs = append(m.logs, fmt.Sprintf("Archivo %d", i))
	}
	lines, _, _ := m.document()
	press(m, tea.KeyUp)
	if m.follow || m.offset != len(lines)-m.bodyHeight()-1 {
		t.Fatal("scrolling from live progress jumped away from the visible tail")
	}
	press(m, tea.KeyEnd)
	if !m.follow {
		t.Fatal("End did not resume following live progress")
	}
}
