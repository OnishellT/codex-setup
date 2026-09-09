package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"codex-setup/internal/installer"
	"codex-setup/internal/ui"
)

func main() {
	if err := runCLI(); err != nil {
		fmt.Fprintln(os.Stderr, "codex-setup:", err)
		os.Exit(1)
	}
}

func runCLI() error { return runCLIArgs(os.Args[1:]) }

type cliOptions struct {
	home, codexHome, modules, models   string
	dry, yes, installDeps, check, list bool
}

func parseCLIOptions(args []string) (cliOptions, error) {
	var o cliOptions
	flags := flag.NewFlagSet("codex-setup", flag.ContinueOnError)
	flags.StringVar(&o.home, "home", "", "Directorio del usuario destino (predeterminado: usuario actual)")
	flags.StringVar(&o.codexHome, "codex-home", "", "CODEX_HOME destino; con --home se usa HOME_DESTINO/.codex salvo override explícito")
	flags.StringVar(&o.modules, "modules", "", "IDs separados por coma; sin esta opción abre el TUI")
	flags.StringVar(&o.models, "models", "", "Archivo JSON de modelo/esfuerzo por rol; opciones verificadas con la cuenta ChatGPT")
	flags.BoolVar(&o.dry, "dry-run", false, "Vista previa sin instalar (con --modules o selección predeterminada)")
	flags.BoolVar(&o.yes, "yes", false, "Confirma cambios de configuración; requiere --modules")
	flags.BoolVar(&o.installDeps, "install-deps", false, "Autoriza instalar dependencias, además de --yes; puede usar sudo")
	flags.BoolVar(&o.check, "check", false, "Comprueba instalación, cuenta y confianza de hooks sin cambiar nada")
	flags.BoolVar(&o.list, "list", false, "Lista módulos disponibles")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("argumentos inesperados: %s", strings.Join(flags.Args(), " "))
	}
	return o, o.validate()
}

func (o cliOptions) validate() error {
	if o.yes && o.modules == "" {
		return fmt.Errorf("--yes requiere --modules; no se selecciona todo implícitamente")
	}
	if o.installDeps && (!o.yes || o.modules == "" || o.dry || o.check) {
		return fmt.Errorf("--install-deps requiere --yes y --modules; no se combina con --dry-run o --check")
	}
	if o.check && (o.yes || o.dry || o.models != "") {
		return fmt.Errorf("--check no se combina con opciones de instalación o modelos")
	}
	if o.models != "" && o.modules == "" && !o.dry {
		return fmt.Errorf("--models requiere --modules o --dry-run; usa m en el TUI")
	}
	return nil
}

func runCLIArgs(args []string) error {
	o, err := parseCLIOptions(args)
	if err == flag.ErrHelp {
		return nil
	}
	if err != nil {
		return err
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("plataforma no soportada: %s/%s; requiere Linux amd64/arm64", runtime.GOOS, runtime.GOARCH)
	}
	if o.codexHome == "" && o.home == "" {
		o.codexHome = os.Getenv("CODEX_HOME")
	}
	data, err := fs.Sub(assets, "payload")
	if err != nil {
		return err
	}
	engine, err := installer.New(data, o.home, o.codexHome)
	if err != nil {
		return err
	}
	if o.list {
		for _, m := range engine.Modules {
			fmt.Printf("%-15s %s\n  %s\n", m.ID, m.Name, m.Description)
		}
		return nil
	}
	if o.modules == "" && !o.dry && !o.check {
		return ui.Run(engine)
	}
	ids := selectedCLIIDs(o.modules, engine.Modules)
	if o.check {
		return reportReadiness(engine, ids)
	}
	return runHeadless(engine, ids, o)
}

func selectedCLIIDs(value string, modules []installer.Module) []string {
	var ids []string
	if value != "" {
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	for _, m := range modules {
		if m.Default {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

func readCLIChoices(path string) (map[string]installer.ModelChoice, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var choices map[string]installer.ModelChoice
	if err := json.Unmarshal(data, &choices); err != nil {
		return nil, fmt.Errorf("JSON de modelos inválido: %w", err)
	}
	return choices, nil
}

func runHeadless(engine *installer.Engine, ids []string, o cliOptions) error {
	choices, err := readCLIChoices(o.models)
	if err != nil {
		return err
	}
	status, err := engine.ReadAccount()
	if err != nil {
		return fmt.Errorf("no se pudo verificar la cuenta ChatGPT: %w; ejecuta codex login y vuelve a intentar", err)
	}
	stop, err := prepareCLIDependencies(engine, ids, o)
	if err != nil || stop {
		return err
	}
	plan, err := engine.BuildPlanWithAccount(ids, choices, status)
	if err != nil {
		return err
	}
	printCLIPlan(engine, plan)
	if o.dry {
		return nil
	}
	if !o.yes {
		return fmt.Errorf("vista previa solamente; añade --yes para confirmar o abre el TUI sin --modules")
	}
	result, err := engine.Apply(plan, func(message string) { fmt.Println(message) })
	if err != nil {
		return err
	}
	fmt.Printf("Archivos instalados: %d. Respaldo: %s\n", result.Changed, result.BackupDir)
	fmt.Println("Abre una terminal nueva: codex / codex --profile work. CODEX_PANEL_DISABLE=1 codex omite el panel.")
	return reportReadiness(engine, ids)
}

func prepareCLIDependencies(engine *installer.Engine, ids []string, o cliOptions) (bool, error) {
	dependencies, err := engine.PlanDependencies(ids)
	if err != nil {
		return false, err
	}
	if !dependencies.NeedsInstall() {
		return false, nil
	}
	fmt.Println("Dependencias pendientes:", strings.Join(dependencies.Missing, ", "))
	for _, command := range dependencies.Commands {
		fmt.Printf("  %s %s\n", command.Path, strings.Join(command.Args, " "))
	}
	for _, warning := range dependencies.Warnings {
		fmt.Println("Aviso:", warning)
	}
	if o.dry {
		fmt.Println("No se instalaron dependencias ni configuración; la vista previa de archivos requiere resolverlas primero.")
		return true, nil
	}
	if !o.installDeps {
		return false, fmt.Errorf("dependencias sin autorizar; usa --yes --install-deps para instalarlas o abre el TUI")
	}
	return false, engine.InstallDependencies(dependencies, os.Stdin, os.Stdout, os.Stderr)
}

func printCLIPlan(engine *installer.Engine, plan *installer.Plan) {
	fmt.Printf("Destino: %s\nCodex: %s\n", engine.Home, engine.CodexHome)
	for _, m := range plan.Modules {
		fmt.Println("Módulo:", m.ID)
	}
	for _, role := range installer.ModelRoles {
		if choice, ok := plan.Models[role.ID]; ok {
			fmt.Printf("Modelo %s: %s/%s\n", role.ID, choice.Model, choice.Effort)
		}
	}
	for _, warning := range plan.Warnings {
		fmt.Println("Aviso:", warning)
	}
	for _, c := range plan.Changes {
		fmt.Printf("%s %s\n", c.Kind, c.Path)
	}
	fmt.Printf("%d archivos cambiarían.\n", len(plan.Changes))
}

func reportReadiness(engine *installer.Engine, ids []string) error {
	r, err := engine.VerifyInstallation(ids)
	if err != nil {
		return err
	}
	if r.Ready() {
		fmt.Println("Listo para usar: archivos, dependencias, modelos y hooks seleccionados verificados.")
		return nil
	}
	for _, pending := range r.Pending {
		fmt.Println("Pendiente:", pending)
	}
	return fmt.Errorf("instalación pendiente de verificación; revisa /hooks en Codex y ejecuta --check con los mismos --modules y destino")
}
