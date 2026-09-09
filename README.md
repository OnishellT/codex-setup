# Codex Setup

Instalador modular local en Go + [Bubble Tea v2](https://github.com/charmbracelet/bubbletea).
Snapshot del setup del 2026-09-03, preparado para copiar a otra PC Linux.
No es un plugin oficial ni reemplaza el ejecutable de Codex CLI. El módulo del
panel integra el comando habitual `codex` mediante una función del shell.
Soporta Linux amd64/arm64. macOS y Windows se rechazan explícitamente.

## Usar

Copia **esta carpeta completa**, incluidos `bin/` y `payload/`, a la otra PC:

```sh
cd ~/projects/codex-setup
./install.sh
```

El menú usa ↑/↓ para moverse y Espacio para marcar módulos. Enter abre una
vista previa. Si faltan dependencias, se muestran los paquetes/comandos y se pide
una confirmación exclusiva para instalarlos (sudo puede solicitar tu contraseña).
Después se revisan y confirman por separado los cambios de configuración.
Puedes volver atrás o cancelar antes de cada confirmación.
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
compilarlo con Go. No se intenta compilar para plataformas no soportadas.

## Requisitos y comprobación final

Parte de Codex CLI ya instalado y `codex login` completado con ChatGPT en el
`CODEX_HOME` destino. El instalador consulta `account/read` y `model/list` del
app-server local; no copia autenticación ni consume una generación del modelo.
Sin cuenta verificable no instala. Un proveedor o catálogo externo configurado
bloquea la comprobación: retíralo conscientemente antes de continuar.

La instalación automática de paquetes usa repositorios nativos de Ubuntu 24.04+,
Debian 12+, Fedora 40+ o Arch rolling. Requiere red y permisos del gestor de
paquetes. RTK y zg usan copias privadas; no se reemplazan ejecutables globales.
Los fallos de red, permisos o paquetes se muestran y bloquean la configuración.

Al terminar distingue **archivos instalados** de **listo para usar**. Los hooks
requieren que revises y concedas confianza en `/hooks` de Codex. El instalador
nunca los confía por ti: pulsa **r** en el resultado para volver a comprobar.
También puedes ejecutar `--check` con la misma selección y destino; devuelve un
error mientras haya archivos, dependencias, modelos o hooks pendientes.

## Módulos

| ID | Qué instala | Selección inicial |
|---|---|---|
| `base` | Astra/medium, tema Dracula y autenticación ChatGPT | Sí |
| `profiles` | `personal.config.toml` con Catppuccin Mocha y `work.config.toml` | Sí |
| `agents` | Hasta 4 agentes, defaults Spark/medium, MultiAgent V2 e instrucciones eager | Sí |
| `prewalk` | Dispatcher compacto + skill automática, executor Spark/medium y reviewer Sol/xhigh | Sí |
| `rtk` | Hook de RTK para reducir la salida de comandos Bash | Sí |
| `ponytail` | Ponytail 4.9.0: tres hooks de simplicidad y seis skills oficiales | Sí |
| `context-handoff` | Alertas de contexto activo y skill `$handoff` compacta | Sí |
| `zg` | MCP de búsqueda semántica sobre un índice local existente | No |
| `panel` | Integración de `codex` en Bash/Zsh, menú lateral, logs, tokens y cuota | Sí |

`prewalk` requiere `agents`; `panel` requiere `agents` y `profiles`; `profiles` requiere `base`.
Desmarcar un módulo no desinstala archivos anteriores.

## Modelos de la suscripción ChatGPT

El setup usa sólo autenticación ChatGPT y el catálogo nativo de Codex. No instala
proveedores externos, gateways, servicios Docker ni credenciales de API.
Al confirmar los módulos de modelos se retiran `model_provider`,
`model_providers` y `model_catalog_json` de los archivos gestionados.
Se habilita MultiAgent V2 y `forced_login_method = "chatgpt"`.

| Rol | Preset |
|---|---|
| Principal | GPT-6 Astra / medium |
| Reviewer | GPT-5.6 Sol / xhigh |
| Executors, explorers y subagente genérico | GPT-5.3 Codex Spark / medium |
| Fallback explorer / executor | GPT-5.6 Luna / medium |

En el TUI pulsa **m** desde Módulos: ↑/↓ elige rol, ←/→ cambia modelo,
Tab cambia esfuerzo y **d** restaura el preset. Las opciones proceden del catálogo
de tu cuenta, no del catálogo incluido en el ejecutable. **c** vuelve a consultar
la cuenta. Enter vuelve a los módulos; la revisión muestra las elecciones
efectivas antes de escribir.

Los valores omitidos usan el preset si está disponible. Si falta Spark, se
prefiere Luna/medium; si tampoco está disponible, se propone el modelo
predeterminado de la cuenta. Los roles de respaldo requieren Luna o una elección
explícita disponible. Una elección explícita no disponible se rechaza.
Esto comprueba acceso al catálogo, no garantiza cuota libre para la siguiente
generación. En ejecución, la política de fallback distribuida sólo permite
reintentar con Luna ante agotamiento explícito de Spark, nunca por fallos de
pruebas, red o calidad del resultado. No cambia el modelo del principal ni consume
un reset. `model_reasoning_summary = "none"` evita el campo incompatible con Spark.

Para automatizar, `--models roles.json` acepta un objeto parcial como:

```json
{"prewalk_executor":{"model":"gpt-5.6-sol","effort":"medium"}}
```

```sh
./install.sh --modules base,prewalk,profiles --models roles.json --dry-run
./install.sh --modules base,prewalk,profiles --models roles.json --yes
```

Los roles omitidos siguen la resolución de cuenta descrita arriba. Modelos o esfuerzos no admitidos se rechazan
antes de escribir. Reinicia Codex después de cambiar configuración. Migrar
configuración no elimina automáticamente servicios o credenciales externos:
retíralos sólo después de comprobar la ruta nativa y terminar sesiones dependientes.

## zg opcional

`./install.sh --modules zg --yes --install-deps` instala una copia privada y
configura MCP con rutas absolutas. Descarga Node 22.23.2 y
`@zvec/zvec-grep@0.2.1`, verifica sus hashes, instala dependencias npm con
`--ignore-scripts`, aplica el parche Linux PR 86 y comprueba bindings nativos.
No usa npm global, no arranca el servidor durante la instalación y no descarga
modelos ni crea, reconstruye o borra índices. Los directorios existentes no
válidos se preservan y se rechazan, en lugar de sobrescribirlos.

El módulo mantiene `enabled = true`, `required = false` y sólo expone
`zvec_grep_search`. Las llamadas siguen las aprobaciones de Codex. Úsalo sólo
con un índice local pertinente y para búsquedas conceptuales; para búsquedas
exactas, ausencia de índice o fallos usa `rg`. Todos los agentes deben enviar
`freshness: "wait_for_fresh"`; descarta `possibly_stale`, `timeout` y `error`.
La instalación no se considera una autorización para crear índices.

Las rutas del runtime, la comprobación/reversión del parche y las operaciones
manuales están en [la guía de zg](payload/integrations/zg/README.md).
No ejecutes `zg install`: administra configuración y aprobaciones fuera de este
setup. Desactivar `[mcp_servers.zvec_grep].enabled` evita arranques futuros;
detener su daemon compartido afecta a otras sesiones.

Ponytail inyecta sus instrucciones por hooks y también instala `ponytail`,
`ponytail-audit`, `ponytail-debt`, `ponytail-gain`, `ponytail-help` y
`ponytail-review` en `$CODEX_HOME/skills`, compartidas por work y personal.
Aparecen en `/skills`. Para cambiar el modo envía `$ponytail lite`,
`$ponytail full`, `$ponytail ultra` o `$ponytail off`; `stop ponytail` lo apaga
en la sesión. RTK sigue como hook: la versión integrada no proporciona una
skill oficial para Codex que instalar.

DFM **no está integrado** y no se presenta como instalable. Las skills oficiales
de `.system` las gestiona Codex; este setup solo empaqueta y registra las seis
skills de Ponytail, sin restaurar otras skills retiradas. El instalador ejecuta
comprobaciones de requisitos e instala la copia gestionada de Qlty incluida por
Prewalk, pero no proporciona credenciales.

Los perfiles heredan la configuración compartida y no representan cuentas
distintas. Su formato y los ajustes de agentes siguen la [referencia oficial de
Codex](https://learn.chatgpt.com/docs/config-file/config-reference).

## Alertas de contexto y `$handoff`

El módulo `context-handoff`, habilitado por defecto, usa el último evento
`token_count` para mostrar la **entrada activa** frente a la ventana del modelo;
no confunde ese valor con los tokens acumulados de toda la tarea. El panel lateral
lo muestra como `Entrada contexto 184k / 258k (71%)`. Un hook avisa una sola vez
al cruzar 70% y otra al cruzar 85%; `/compact` reinicia esos avisos. Los umbrales
y el máximo de 12 000 caracteres se pueden cambiar en
`$CODEX_HOME/integrations/handoff/settings.json`. El instalador crea ese archivo
sólo si falta y conserva personalizaciones posteriores. GPT-5.6 Sol además avisa
preventivamente a 250 000 tokens y marca crítico a 272 000, antes del recargo por
contexto largo documentado para solicitudes con más de 272K tokens; los demás
modelos usan los porcentajes generales. Ese umbral refleja precios de API; una
suscripción o un gateway puede contabilizar la cuota de otra manera.

`$handoff` crea un Markdown privado y acotado bajo
`$CODEX_HOME/handoffs/<proyecto>/<id>.md`, captura el estado Git y pide completar
sólo objetivo, decisiones, trabajo hecho/pendiente, archivos, validaciones,
riesgos y próximo paso. No copia conversaciones, razonamiento oculto, secretos,
salidas grandes ni diffs completos. Después de validarlo devuelve un comando de
esta forma:

```sh
codex -C '/ruta/al/proyecto' '$handoff resume <id>'
```

La nueva tarea relee las reglas y archivos vivos y compara proyecto, rama, HEAD,
estado sucio y worktrees antes de continuar. No archiva la tarea anterior, no
hace commit/push y no inicia otra instancia por sí sola. Las alertas requieren
revisar y confiar el hook con `/hooks`; la skill sigue disponible aunque el hook
no esté confiado o el panel esté desactivado.

## Prewalk automático

`./install.sh --modules prewalk --yes` añade un dispatcher gestionado y compacto a
`developer_instructions` en `$CODEX_HOME/config.toml`, conserva las instrucciones
anteriores e instala la skill automática `$CODEX_HOME/skills/prewalk/SKILL.md`,
registrada como enabled, además de los roles de exploración, ejecución/fallback y
`agents/engineering_reviewer.toml`,
helpers de Git/Qlty y hooks de revisión. Requiere Python 3.11+, Git, `tar` con
soporte xz y conexión en la primera instalación: tras el consentimiento de
dependencias descarga Qlty 0.644.0 desde su release oficial, verifica
su SHA-256 y lo instala en `$CODEX_HOME/integrations/prewalk/bin`. En Linux usa
el binario musl estático, compatible también con sistemas glibc. No ejecuta
scripts remotos; la previsualización no descarga Qlty. Qlty descarga los analizadores necesarios al preparar o analizar
un proyecto. Qlty usa BSL/Fair Source: revisa su licencia si ofrecerás este setup
como servicio a terceros. No necesita
panel, Node ni plugins de Codex. El módulo `agents`, dependencia de
Prewalk, aplica el preset nativo incluido el principal Astra/medium. La selección
de modelos no altera las aprobaciones; el módulo Prewalk añade sus defaults sólo
cuando no hay una elección de permisos. Revisa y confía el hook con `/hooks`:
habilitar hooks no equivale a confiar en ellos.
Funciona con cualquier `CODEX_HOME` soportado por el instalador.

Al comenzar una implementación, el planner prepara Git y ejecuta `quality.py setup`.
Si ya existe `.qlty/qlty.toml`, lo conserva y valida; si falta, Qlty detecta el
proyecto y el helper confirma únicamente su configuración en un commit separado,
dejando una baseline limpia que comparten los worktrees. El planner contrasta lo
detectado con manifests y configuraciones existentes. En un repositorio todavía
vacío, repite esa revisión cuando aparezcan los manifests del lenguaje. Todas las
ejecuciones fuerzan `QLTY_TELEMETRY=off` y desactivan el chequeo de actualizaciones.
El helper usa primero la copia gestionada por el instalador y recurre a `qlty` de
`PATH` sólo si aquella no existe. `quality` está habilitado cuando vale `true` o
no existe en `settings.json`; ponlo
en `false` para desactivarlo conscientemente. La versión local validada es Qlty
0.644.0 y cada reporte registra la versión realmente utilizada.

En tareas de implementación no triviales, el orquestador explora y prepara un
plan breve, delega la ejecución y pruebas, obtiene una revisión independiente
y verifica el resultado. El revisor también se invoca si el principal implementó
directamente un cambio no trivial. No requiere
que el usuario controle el cierre: el principal debe esperar la finalización
confirmada de sus agentes; recibir un informe no equivale a que hayan terminado.
No requiere `$prewalk` ni pedirlo en cada prompt: el dispatcher carga la skill
automáticamente sólo para las solicitudes que cumplen sus disparadores.
Consultas, planes sin autorización para implementar y cambios mínimos se
resuelven directamente. Es una política nativa de instrucciones y skill, no el
cambio de modelo dentro de la misma sesión de OMP.

Work y personal heredan la activación compartida. Para apagar sólo Prewalk,
retira de `developer_instructions` el bloque entre `<!-- codex-setup:prewalk -->`
y `<!-- /codex-setup:prewalk -->`, preservando el resto, y establece en
`skills.config` la entrada de `$CODEX_HOME/skills/prewalk/SKILL.md` con
`enabled = false`. No apagues todos los subagentes. Reinstalar el módulo vuelve
a activarlo. Un perfil que redefine `developer_instructions` debe conservar ese
bloque y la entrada enabled si quiere usar Prewalk.

El módulo usa las operaciones gestionadas `developer-instructions`, `template-copy`,
`skills-state`, `agent-instructions`, `tree`, `qlty-install`,
`prewalk-settings`, `prewalk-config`, `native-config` y `hooks-state`.

Los modelos elegidos en el instalador se aplican explícitamente a cada rol.
Se conservan instrucciones y permisos personalizados; las instrucciones generadas
conocidas se actualizan. Los fallbacks son roles explícitos y pueden configurarse
con un modelo nativo diferente; el preset usa Spark para ejecución/exploración
y Luna/medium para los dos respaldos.
El hook global se actualiza sin borrar hooks ajenos. Los scripts de
`integrations/prewalk` se actualizan con respaldo; personaliza settings y roles,
no las copias gestionadas de scripts.

### Workers paralelos en worktrees

El planner investiga reglas/patrones y entrega contratos: objetivo, interfaz,
archivos permitidos, exclusiones, dependencias y aceptación; no escribe el código
línea por línea. Hasta cuatro contratos independientes pueden ejecutarse en
paralelo con `prewalk_executor`, cada uno en su rama/worktree. El planner usa el
menor número de workers que cubra el trabajo independiente. Cada comando y edición debe
usar la ruta absoluta asignada; los subagentes nativos no cambian automáticamente
de directorio por una instrucción `cd` anterior. Es separación de checkouts, no
un sandbox entre workers: puertos, DB, credenciales y procesos siguen compartidos.
Para investigación read-only no trivial, dos o más líneas independientes se
delegan simultáneamente a explorers; el principal conserva decisiones y síntesis.
Si sólo usa uno, debe explicar qué impidió una segunda línea útil.

`$CODEX_HOME/integrations/prewalk/settings.json` contiene `worktrees: true` y
`max_workers: 4`. Es configuración del **setup**, no claves inventadas de Codex.
Reinstalar migra la plantilla anterior de 2 a 4 y conserva archivos personalizados.
Usa `max_workers: 1` para ejecución serial aislada. También se respeta la
concurrencia nativa. Todo writer delegado requiere manifest/worktree; deshabilitar
worktrees no autoriza writers en el checkout compartido. Las explicaciones y
cambios triviales que no activan Prewalk pueden resolverse directamente.

Antes de escribir archivos de un proyecto nuevo, Prewalk ejecuta
`python3 "$CODEX_HOME/integrations/prewalk/worktrees.py" prepare --repo RUTA`.
Si la carpeta está vacía y fuera de Git, inicializa el repositorio y crea un
commit con un `.gitignore` mínimo; no agrega secretos ni otros archivos.
Reutiliza repositorios existentes, incluidos los de una carpeta padre, sin crear
repositorios anidados. Usa tu identidad Git configurada: si falta, avisa sin
inventarla ni cambiar configuración global. Una carpeta no vacía sin Git requiere
inspeccionar y autorizar explícitamente qué archivos formarán la base inicial;
no se hace `git add .` automático. Consultas, revisiones y planes sin autorización
para implementar no inicializan Git. No requiere GitHub ni un remoto.
Al probarlo con `codex exec` desde una carpeta sin Git, usa
`--skip-git-repo-check` para que el CLI permita arrancar antes de la preparación;
esto no concede permisos de escritura ni sustituye la identidad Git.

El helper `integrations/prewalk/worktrees.py` ofrece `prepare --repo RUTA`,
`create --repo RUTA --workers N --max-workers 4`,
`integrate --manifest RUTA` y `finish --manifest RUTA`. Crea un manifest y worktrees
bajo `$CODEX_HOME/worktrees/prewalk`, con permisos privados `0700`
(`--session-parent` permite otra carpeta existente fuera del checkout).
Exige baseline limpio/committeado, rama y ausencia de operaciones Git pendientes;
no admite submódulos. No hace stash, push ni descarta cambios. Instrucciones locales
ignoradas no se copian: el planner debe comprobar los avisos y aportar las reglas
aplicables. Si faltan requisitos, informa y se detiene sin writers compartidos.
Antes de integrar, compara los paths modificados por todos los workers y se detiene
sin aplicar commits si dos tocaron el mismo archivo. Ese archivo debe replanificarse
en serie; nunca se elige un ganador automático.
Crear ramas y commits requiere escritura en los metadatos Git. `workspace-write`
puede mantener `.git` de sólo lectura: se usa la aprobación normal para esa operación
si está disponible; si se deniega o no puede solicitarse, se informa y se detiene.
El instalador configura `workspace-write`, `on-request` y `auto_review` cuando
no existen elecciones de permisos, y añade sólo la raíz privada escribible.
No habilita `--yolo` y conserva permisos personalizados con un aviso.

Antes de cada spawn, el planner ejecuta `writer_guard.py register --manifest RUTA
--worker worker-1 --task nombre_de_tarea`. El registro queda vinculado al
`CODEX_THREAD_ID` nativo y al nombre de tarea. V2 cifra los mensajes antes del hook;
el guard comprueba este registro sin descifrarlos, valida el worktree y reserva
cada worker una sola vez. Un fallback usa `--role fallback_executor` y un worktree
nuevo. Los hooks requieren revisión de confianza nativa; no son aislamiento de SO.

Workers entregan commits y pruebas. El principal comprueba el alcance, integra en
orden, ejecuta pruebas conjuntas y solicita revisión del resultado combinado. Los
conflictos se preservan para resolución explícita. `finish` sólo hace fast-forward
si el checkout original sigue limpio, en la rama/base inicial y todos los workers
están integrados. Las correcciones de review se realizan sobre el código integrado.
No hay limpieza automática: los worktrees y ramas de la sesión quedan recuperables.

Cada worker confirma sólo sus cambios y verifica que su worktree esté limpio
antes de ejecutar `quality.py scan`. La integración ejecuta el mismo scan contra el SHA
base exacto. El helper reúne lint en SARIF, smells en SARIF y métricas por función
en JSON, sin autofix ni IA. Una herramienta ausente, error o salida inválida no se
interpreta como pass. El reviewer recibe el reporte normalizado para enfocar su
inspección, pero la complejidad ciclomática/cognitiva no se presenta como una
medición de CPU, memoria o latencia. Las pruebas y benchmarks propios del proyecto
siguen siendo obligatorios cuando el riesgo lo justifica.
Los reportes se escriben fuera del repositorio y registran HEAD, upstream, hash de
configuración y versión de Qlty para detectar evidencia obsoleta o del worktree
equivocado.

### Revisión adversarial de ingeniería

`engineering_reviewer` es un rol separado del implementador, utilizable también
para una revisión explícita fuera de Prewalk. Recibe petición original, plan,
criterios de aceptación, base del diff y ubicaciones del código y pruebas;
inspecciona el código actual y las instrucciones aplicables del proyecto.
Se crea con `fork_turns="none"` (`fork_context=false` en la API antigua), sin
conversaciones, resúmenes ni justificaciones del implementador. Cada re-review
también parte de contexto nuevo. El hook global `PreToolUse` rechaza crear este
rol sin una elección explícita de contexto limpio. El mismo hook filtra por
`agent_type=engineering_reviewer` (verificado en CLI 0.153.2) y
bloquea accesos detectados a sesiones, archivos de historial, memorias, bases de
datos de historial y herramientas de consulta de conversaciones; no lee sus contenidos.
Resuelve rutas/symlinks evidentes y funciona con un `CODEX_HOME` personalizado.

Es una protección práctica, **no aislamiento de seguridad absoluto**: un filtro
puede omitir accesos indirectos u ofuscados; herramientas especializadas pueden
omitir hooks, y `write_stdin` no repite `PreToolUse`. Si faltan Python, confianza
o la metadata de rol, la revisión protegida no está verificada y debe informarse. No se
instala un bypass de confianza ni se alteran sandbox/aprobaciones.

Comprueba ocho áreas: propósito, corrección, reglas del proyecto, rendimiento,
diseño, seguridad/fiabilidad, pruebas e integración. Devuelve hallazgos P0-P3 con
ubicación, escenario, impacto, evidencia y sugerencia; cada área queda marcada
como `verified`, `finding`, `N/A` o `not verified`. Puede cuestionar el plan y no
confunde ausencia de hallazgos con demostración de corrección. No exige benchmarks
triviales ni bloquea por preferencias de estilo.

El principal espera a que termine el ejecutor y el diff esté estable antes de
revisar; resuelve los hallazgos y solicita revisión de las correcciones y sus
interacciones. Si falta el revisor o evidencia esencial, lo informa sin simular
una aprobación. Se mantienen hasta cuatro agentes y una sola profundidad.

El rol pide `sandbox_mode = "read-only"`, deshabilita su propia delegación y tiene
instrucciones de no modificar la solución. Si una prueba requiere escrituras,
solicita al principal ejecutarla. No modifica aprobaciones. Los overrides del
arranque, incluidos `--sandbox workspace-write` y `--yolo`, pueden prevalecer
sobre el sandbox del rol (observado en una prueba CLI): esto no es
una barrera de seguridad independiente. La invocación y checklist son instrucciones
para el modelo, no un bloqueo determinista de CI ni una garantía de calidad.

La configuración usa [instrucciones nativas](https://learn.chatgpt.com/docs/config-file/config-reference)
y [agentes personalizados](https://learn.chatgpt.com/docs/agent-configuration/subagents).

## Dependencias del panel

- Linux con `/proc` accesible y una terminal interactiva; recomendado 120 columnas.
- Codex CLI en PATH. Versión probada: **0.153.0**. Inicia sesión por separado.
- Python **3.11+**, con `curses`, colores extendidos, `sqlite3` y `tomllib`.
- tmux real **>= 3.3**; validado con **3.7b**. No sirve un shim de tmux.
- `tic` de ncurses para generar la definición de terminal local.

El instalador ofrece instalar estas dependencias mediante el gestor nativo,
con confirmación separada antes de ejecutar sudo. No instala Codex, no crea
conversaciones ni solicita generaciones. Si tu tmux está en una ruta especial:

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
agente y **solo Enter** para cerrar el flotante. Al terminar Codex, el supervisor
cierra automáticamente la sesión tmux aislada y su monitor; Ctrl-b d sigue siendo
un simple detach reconectable. No modifica `.tmux.conf` ni otras sesiones. Más
detalles en [payload/panel/README.md](payload/panel/README.md).

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
./install.sh --modules rtk,ponytail --yes --install-deps
./install.sh --modules context-handoff --yes --install-deps

# Revalidación sin reinstalar ni conceder confianza
./install.sh --modules rtk,ponytail --check

make test
make check
```

`--home` ignora el `CODEX_HOME` heredado para evitar tocar accidentalmente la
cuenta real durante pruebas. `--codex-home` permite un destino explícito distinto.
Un destino aislado sin login propio se rechaza; no copies credenciales para probar.
Los tests Go usan directorios temporales y un app-server simulado para la cuenta. La prueba de integración instala el
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
- `native-config`: aplicar la matriz nativa validada y retirar configuración externa.
- `prewalk-config`: permisos nativos y raíz privada para worktrees.
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
