# Codex Setup

Instalador modular local en Go + [Bubble Tea v2](https://github.com/charmbracelet/bubbletea).
Snapshot del setup del 2026-09-03, preparado para copiar a otra PC Linux.
No es un plugin oficial ni reemplaza el ejecutable de Codex CLI. El módulo del
panel integra el comando habitual `codex` mediante una función del shell.
No soporta Windows.

## Usar

Copia **esta carpeta completa**, incluidos `bin/` y `payload/`, a la otra PC:

```sh
cd ~/projects/codex-setup
./install.sh
```

El menú usa ↑/↓ para moverse y Espacio para marcar módulos. Enter abre una
vista previa; un segundo Enter confirma la instalación. Las dependencias se
incluyen automáticamente. Puedes volver atrás o cancelar antes de confirmar.
La ayuda de cada pantalla muestra sus teclas. El menú está en español; la UI del
panel conserva su comportamiento actual (inglés por defecto, logs sin traducir).

Los binarios Linux amd64/arm64 incluidos no requieren Go ni descargar módulos al
instalar. También contienen el payload: puedes trasladar solo el binario de tu
arquitectura. Para recompilar o añadir módulos se necesita Go 1.26+ y acceso a las
dependencias de `go.mod` / `go.sum` la primera vez:

```sh
make build
# ambos binarios Linux:
make release
```

`install.sh` usa el binario existente; después de editar el código/payload debes
recompilar con `make build` o `make release`. Si falta el binario, el script intenta
compilarlo con Go. En macOS los módulos de archivos son utilizables compilando;
el panel Linux se rechaza explícitamente. macOS no está validado.

## Módulos

| ID | Qué instala | Selección inicial |
|---|---|---|
| `base` | Sol/high, tema Dracula global y flags del setup actual | Sí |
| `profiles` | `personal.config.toml` con Catppuccin Mocha y `work.config.toml` | Sí |
| `agents` | Configuración Terra/medium, máximo 2 agentes e instrucciones eager | Sí |
| `prewalk` | Orquestador automático desde config + ejecutor nativo Terra/medium | Sí |
| `rtk` | Hook de RTK para reducir la salida de comandos Bash | Sí |
| `ponytail` | Ponytail 4.9.0: tres hooks de simplicidad y seis skills oficiales | Sí |
| `panel` | Integración de `codex` en Bash/Zsh, menú lateral, logs, tokens y cuota | Sí |

`prewalk` requiere `agents`; `panel` requiere `agents` y `profiles`; `profiles` requiere `base`;
las dependencias se resuelven incluso en modo CLI. Desmarcar un módulo **no
desinstala** instalaciones anteriores.

`rtk` y `ponytail` son independientes de `base` y de los perfiles: puedes
instalarlos sin restablecer modelo, tema ni ajustes personales existentes. Ambos
escriben únicamente sus grupos identificados en `$CODEX_HOME/hooks.json`,
preservan hooks ajenos y activan sólo `features.hooks = true`; no habilitan
plugins. Codex pedirá la confianza de cada hook: después de instalar, revisa y
aprueba los marcados `[codex-setup:rtk]` y `[codex-setup:ponytail]` mediante
`/hooks`. El instalador no usa `--dangerously-bypass-hook-trust` ni auto-confía
los hooks.

RTK exige Python 3 y `rtk >= 0.23` disponible en el PATH de Codex. Ponytail
exige Python 3 y Node.js >= 18. No se descargan binarios, dependencias npm ni
plugins. Consulta los README instalados en `$CODEX_HOME/integrations/rtk/` y
`$CODEX_HOME/integrations/ponytail/` para la desactivación y sus límites.

Ponytail inyecta sus instrucciones por hooks y también instala `ponytail`,
`ponytail-audit`, `ponytail-debt`, `ponytail-gain`, `ponytail-help` y
`ponytail-review` en `$CODEX_HOME/skills`, compartidas por work y personal.
Aparecen en `/skills`. Para cambiar el modo envía `$ponytail lite`,
`$ponytail full`, `$ponytail ultra` o `$ponytail off`; `stop ponytail` lo apaga
en la sesión. RTK sigue como hook: la versión integrada no proporciona una
skill oficial para Codex que instalar.

DFM **no está integrado** y no se presenta como instalable. Las skills oficiales
de `.system` las gestiona Codex; este setup solo empaqueta y registra las seis
skills de Ponytail, sin restaurar otras skills retiradas. El instalador no ejecuta scripts adicionales, instala dependencias
ni proporciona credenciales.

Los perfiles heredan la configuración compartida y no representan cuentas
distintas. Su formato y los ajustes de agentes siguen la [referencia oficial de
Codex](https://learn.chatgpt.com/docs/config-file/config-reference).

## Prewalk automático

`./install.sh --modules prewalk --yes` añade un bloque gestionado a
`developer_instructions` en `$CODEX_HOME/config.toml`, conserva las instrucciones
anteriores e instala `agents/prewalk_executor.toml`. No cambia el modelo principal,
los perfiles ni los hooks; no necesita panel, Python, Node, plugins ni una skill.
Funciona con cualquier `CODEX_HOME` soportado por el instalador.

En tareas de implementación no triviales, el orquestador explora y prepara un
plan breve, delega la ejecución y pruebas, y verifica el resultado. No requiere
que el usuario controle el cierre: el principal debe esperar la finalización
confirmada de sus agentes; recibir un informe no equivale a que hayan terminado.
No requiere
`/prewalk` ni pedirlo en cada prompt. Consultas, planes sin autorización para
implementar y cambios mínimos se resuelven directamente. Es una política nativa
de instrucciones, no el cambio de modelo dentro de la misma sesión de OMP.

Work y personal heredan la activación compartida. Para apagar sólo Prewalk,
retira de `developer_instructions` el bloque entre `<!-- codex-setup:prewalk -->`
y `<!-- /codex-setup:prewalk -->`, preservando el resto. No apagues todos los
subagentes. Reinstalar el módulo vuelve a activarlo. Un perfil que redefine
`developer_instructions` debe conservar ese bloque si quiere usar Prewalk.

El TOML del ejecutor se crea sólo si no existe: reinstalar conserva tus cambios
de modelo, proveedor e instrucciones (también conserva una plantilla antigua;
las actualizaciones de ese rol se revisan manualmente).
El modelo del ejecutor se configura en su TOML; inicialmente usa Terra/medium e
hereda el proveedor. Para futuros proveedores, configura modelo y proveedor como
pareja mediante las opciones nativas y verifica su compatibilidad antes de usar
datos reales. Prewalk no añade un router ni copia credenciales.

La configuración usa [instrucciones nativas](https://learn.chatgpt.com/docs/config-file/config-reference)
y [agentes personalizados](https://learn.chatgpt.com/docs/agent-configuration/subagents).

## Dependencias del panel

- Linux con `/proc` accesible y una terminal interactiva; recomendado 120 columnas.
- Codex CLI en PATH. Versión probada: **0.153.0**. Inicia sesión por separado.
- Python **3.11+**, con `curses`, colores extendidos, `sqlite3` y `tomllib`.
- tmux real **>= 3.3**; validado con **3.7b**. No sirve un shim de tmux.
- `tic` de ncurses para generar la definición de terminal local.

Ejemplos de dependencias del sistema (ejecútalos tú si hacen falta):

```sh
# Debian/Ubuntu (verifica que python3 sea >= 3.11)
sudo apt install python3 tmux ncurses-bin
# Fedora
sudo dnf install python3 tmux ncurses
# Arch
sudo pacman -S python tmux ncurses
```

El instalador **no ejecuta sudo**, no cambia el PATH, no instala Codex, no crea
conversaciones ni consume tokens. Si tu tmux está en una ruta especial:

```sh
CODEX_PANEL_TMUX=/ruta/al/tmux ./install.sh
```

La vista previa comprueba dependencias y compila terminfo en un directorio
temporal que elimina; todavía no escribe en los destinos. La ruta local de
Python/tmux se guarda en el lanzador generado. **No copies después ese lanzador a
otro equipo**: vuelve a ejecutar el instalador allí.

Tras instalar, abre una terminal nueva. El comando normal abre el panel:

```sh
codex --profile personal
codex --profile work
codex --yolo
codex -p work --yolo
codex resume --last
# El lanzador explícito sigue disponible:
codex-panel --profile personal
codex-panel --profile work --cwd /ruta/al/proyecto
```

La integración conserva los argumentos originales, incluido `--yolo`; no añade
permisos ni fuerza `personal` cuando ejecutas `codex` sin perfil. `--yolo` conserva
su significado original de omitir sandbox/aprobaciones. La selección de perfil
también se usa para el tema del panel. `-C` mantiene su semántica de directorio.

`exec`, `app-server`, `login`, `--help`, `--version` y demás comandos de servicio
van directamente al CLI oficial. También se omite el panel sin TTY, dentro de un
panel existente, en modo remoto/efímero o ante opciones desconocidas. Para omitirlo
manualmente: `CODEX_PANEL_DISABLE=1 codex` o `command codex`.

El instalador añade un bloque identificado a `.bashrc` y `.zshrc`. Si `.zshrc`
es un enlace administrado por Nix/Home Manager, utiliza `.zshenv` sin tocar el
enlace; el bloque solo actúa en shells interactivos. Respeta `ZDOTDIR` del usuario
actual, sin heredarlo en una instalación aislada con `--home`. Para activar el
cambio en una terminal que ya estaba abierta, ejecuta `source` sobre el archivo
indicado en la vista previa (`~/.zshenv` en este equipo con Nix).

No reemplaza el binario/enlace `codex` del gestor de paquetes. El comando apunta a
un adaptador `~/.local/bin/codex-with-panel` que llama al ejecutable original.
La integración automática cubre Bash y Zsh; otros shells pueden seguir usando
ese adaptador o `codex-panel` explícitamente. Para quitar la integración, elimina
solo el bloque `codex-setup:launcher` de los archivos del shell o restaura su
respaldo; deja intactos los demás contenidos. No se instala un alias de `--yolo`.

El panel conserva Ctrl-S para ocultar/mostrar, flechas + Enter para abrir un
agente y **solo Enter** para cerrar el flotante. No modifica `.tmux.conf` ni otras
sesiones. Más detalles en [payload/panel/README.md](payload/panel/README.md).

La consulta de cuota ocurre al abrir el panel, no al instalar. Para desactivarla:
`CODEX_PANEL_QUOTA_OFFLINE=1 codex-panel -p personal`. La estimación por agente no
es una medición oficial y su tabla requiere revalidación después del 2026-11-21.
Los formatos internos de Codex pueden cambiar; valida nuevas versiones del CLI.
La apariencia depende también del tema del emulador de terminal del equipo destino.

## Seguridad y respaldo

- No incluye autenticación, historiales/JSONL, bases SQLite, caches, procesos,
  proyectos confiables, rutas de Nix ni el binario de Codex.
- Destinos: `~/.codex`, `~/.local/share/codex-panel`, los lanzadores en
  `~/.local/bin` y el bloque de integración en los archivos del shell indicados.
  Respeta `CODEX_HOME` al instalar normalmente.
- Combina las claves TOML seleccionadas; conserva valores ajenos, pero **puede
  reformatear y quitar comentarios**. Los valores gestionados del módulo elegido
  sí se actualizan. Las instrucciones se añaden mediante un bloque identificado.
- Antes de escribir guarda originales y un `manifest.json` en
  `~/.local/state/codex-setup/backups/<fecha-id>/`, con permisos privados.
- Reinstalar sin cambios no duplica instrucciones ni hooks gestionados, y no
  crea respaldos innecesarios. No borra archivos ajenos o sobrantes.
- Rechaza enlaces simbólicos en los destinos y cambios posteriores a la vista
  previa. Si falla una escritura, intenta restaurar las anteriores. No protege
  contra un corte de energía o `kill -9`: para eso quedan los respaldos.
- Para restaurar manualmente, consulta el manifiesto: `path` es el destino y
  `file` contiene su original. Si `existed=false`, era un archivo nuevo. Detén el
  panel antes de restaurar sus archivos; no borres una carpeta compartida completa.

## Automatización y pruebas

```sh
./install.sh --list
./install.sh --modules agents,profiles --dry-run
./install.sh --modules rtk,ponytail --yes

# Prueba aislada (no cambia el HOME real ni copia autenticación)
destino_prueba=$(mktemp -d)
./install.sh --home "$destino_prueba" --modules rtk,ponytail --yes
./install.sh --home "$destino_prueba" --modules rtk,ponytail --dry-run

make test
make check
```

`--home` ignora el `CODEX_HOME` heredado para evitar tocar accidentalmente la
cuenta real durante pruebas. `--codex-home` permite un destino explícito distinto.
Los tests Go usan directorios temporales. La prueba de integración instala el
panel en uno de ellos (incluido terminfo) y ejecuta allí su suite Python; requiere
las dependencias del panel. No se ejecuta la suite sobre un payload sin compilar.
Algunos tests opcionales del panel
necesitan un historial local concreto o el binario exacto para verificar paletas;
se omiten si faltan. Las pruebas tmux usan servidores propios.

## Añadir módulos

No hay framework de plugins ni servicio residente: el catálogo es
`payload/modules.json`. Añade un ID único, nombre, descripción y operaciones,
coloca sus archivos bajo `payload/`, recompila y aparecerá en el TUI.

Operaciones disponibles:

- `copy`: archivo literal; `tree`: árbol de archivos.
- `copy-if-missing`: instalar una plantilla sólo cuando el destino no existe.
- `developer-instructions`: añadir/actualizar un bloque dentro de la clave nativa
  `developer_instructions` de un TOML, preservando instrucciones ajenas.
- `merge`: combinar un fragmento TOML con el destino.
- `append`: añadir/reemplazar un bloque gestionado en un archivo de instrucciones.
- `hooks-state`: combinar grupos de `hooks.json` propiedad del módulo, sin
  reemplazar hooks ajenos, y activar el flag de hooks al final del plan.
- `panel`: adaptación específica del lanzador, chequeos y terminfo.

Raíces permitidas: `home`, `codex`, `skills`, `data` y `bin`. Los destinos son relativos;
no se permite escapar con `..`. `depends` declara dependencias y `platforms`
restringe sistemas. No se ejecutan comandos arbitrarios desde el manifiesto.

Ejemplo de módulo nuevo:

```json
{
  "id": "mis-instrucciones",
  "name": "Mis instrucciones",
  "description": "Reglas compartidas opcionales",
  "operations": [
    {"kind": "append", "source": "extras/reglas.md", "root": "codex", "target": "AGENTS.md"}
  ]
}
```

## Procedencia

Código del panel: setup local desarrollado en esta conversación. Perfiles e
instrucciones: configuración local seleccionada, sin permisos ni historial.
Ponytail conserva su aviso MIT y procedencia junto a su adaptación de hooks.
Esta es una copia privada de tu setup, no una afirmación de licencia universal
sobre su contenido. Go/Bubble Tea y sus dependencias conservan las licencias
correspondientes.
