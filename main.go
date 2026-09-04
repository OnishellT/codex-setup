package main

import (
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
	home := flag.String("home", "", "Directorio del usuario destino (predeterminado: usuario actual)")
	codexHome := flag.String("codex-home", "", "CODEX_HOME destino; con --home se usa HOME_DESTINO/.codex salvo override explícito")
	modules := flag.String("modules", "", "IDs separados por coma; sin esta opción abre el TUI")
	dry := flag.Bool("dry-run", false, "Vista previa sin instalar (con --modules o selección predeterminada)")
	yes := flag.Bool("yes", false, "Confirma instalación no interactiva; requiere --modules")
	list := flag.Bool("list", false, "Lista módulos disponibles")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("argumentos inesperados: %s", strings.Join(flag.Args(), " "))
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Windows no está soportado")
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
	if *modules == "" && !*dry {
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
	plan, err := engine.BuildPlan(ids)
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
	fmt.Printf("Instalación lista: %d archivos. Respaldo: %s\n", result.Changed, result.BackupDir)
	fmt.Println("Abre una terminal nueva. Uso: codex --yolo / codex --profile work. CODEX_PANEL_DISABLE=1 codex omite el panel.")
	return nil
}
