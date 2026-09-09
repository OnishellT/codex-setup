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
func runCLI() error {
	return runCLIArgs(os.Args[1:])
}

func runCLIArgs(args []string) error {
	flags := flag.NewFlagSet("codex-setup", flag.ContinueOnError)
	home := flags.String("home", "", "Directorio del usuario destino (predeterminado: usuario actual)")
	codexHome := flags.String("codex-home", "", "CODEX_HOME destino; con --home se usa HOME_DESTINO/.codex salvo override explícito")
	modules := flags.String("modules", "", "IDs separados por coma; sin esta opción abre el TUI")
	dry := flags.Bool("dry-run", false, "Vista previa sin instalar (con --modules o selección predeterminada)")
	yes := flags.Bool("yes", false, "Confirma cambios de configuración; requiere --modules")
	installDeps := flags.Bool("install-deps", false, "Autoriza instalar dependencias, además de --yes; puede usar sudo")
	check := flags.Bool("check", false, "Comprueba instalación, cuenta y confianza de hooks sin cambiar nada")
	list := flags.Bool("list", false, "Lista módulos disponibles")
	models := flags.String("models", "", "Archivo JSON de modelo/esfuerzo por rol; opciones verificadas con la cuenta ChatGPT")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("argumentos inesperados: %s", strings.Join(flags.Args(), " "))
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("plataforma no soportada: %s/%s; requiere Linux amd64/arm64", runtime.GOOS, runtime.GOARCH)
	}
	if *installDeps && (!*yes || *modules == "" || *dry || *check) {
		return fmt.Errorf("--install-deps requiere --yes y --modules; no se combina con --dry-run o --check")
	}
	if *check && (*yes || *dry || *models != "") {
		return fmt.Errorf("--check no se combina con opciones de instalación o modelos")
	}
	chosenHome := *codexHome
	if chosenHome == "" && *home == "" {
		chosenHome = os.Getenv("CODEX_HOME")
	}
	data, err := fs.Sub(assets, "payload")
	if err != nil {
		return err
	}
	engine, err := installer.New(data, *home, chosenHome)
	if err != nil {
		return err
	}
	if *list {
		for _, m := range engine.Modules {
			fmt.Printf("%-15s %s\n  %s\n", m.ID, m.Name, m.Description)
		}
		return nil
	}
	if *yes && *modules == "" {
		return fmt.Errorf("--yes requiere --modules; no se selecciona todo implícitamente")
	}
	if *modules == "" && !*dry && !*check {
		if *models != "" {
			return fmt.Errorf("--models requiere --modules o --dry-run; usa m en el TUI")
		}
		return ui.Run(engine)
	}
	ids := []string{}
	if *modules != "" {
		for _, id := range strings.Split(*modules, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	} else {
		for _, m := range engine.Modules {
			if m.Default {
				ids = append(ids, m.ID)
			}
		}
	}
	if *check {
		return reportReadiness(engine, ids)
	}
	var choices map[string]installer.ModelChoice
	if *models != "" {
		b, readErr := os.ReadFile(*models)
		if readErr != nil {
			return readErr
		}
		if err = json.Unmarshal(b, &choices); err != nil {
			return fmt.Errorf("JSON de modelos inválido: %w", err)
		}
	}
	status, err := engine.ReadAccount()
	if err != nil {
		return fmt.Errorf("no se pudo verificar la cuenta ChatGPT: %w; ejecuta codex login y vuelve a intentar", err)
	}
	dependencies, err := engine.PlanDependencies(ids)
	if err != nil {
		return err
	}
	if dependencies.NeedsInstall() {
		fmt.Println("Dependencias pendientes:", strings.Join(dependencies.Missing, ", "))
		for _, command := range dependencies.Commands {
			fmt.Printf("  %s %s\n", command.Path, strings.Join(command.Args, " "))
		}
		for _, warning := range dependencies.Warnings {
			fmt.Println("Aviso:", warning)
		}
		if *dry {
			fmt.Println("No se instalaron dependencias ni configuración; la vista previa de archivos requiere resolverlas primero.")
			return nil
		}
		if !*installDeps {
			return fmt.Errorf("dependencias sin autorizar; usa --yes --install-deps para instalarlas o abre el TUI")
		}
		if err := engine.InstallDependencies(dependencies, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}
	plan, err := engine.BuildPlanWithAccount(ids, choices, status)
	if err != nil {
		return err
	}
	fmt.Printf("Destino: %s\nCodex: %s\n", engine.Home, engine.CodexHome)
	for _, m := range plan.Modules {
		fmt.Println("Módulo:", m.ID)
	}
	for _, warning := range plan.Warnings {
		fmt.Println("Aviso:", warning)
	}
	for _, c := range plan.Changes {
		fmt.Printf("%s %s\n", c.Kind, c.Path)
	}
	fmt.Printf("%d archivos cambiarían.\n", len(plan.Changes))
	if *dry {
		return nil
	}
	if !*yes {
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
