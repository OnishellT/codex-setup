# Guía de uso

[Volver al README](../README.md)

## Instalación y automatización

Los ejemplos usan `./install.sh` desde un checkout. Con un binario descargado,
sustitúyelo por `./codex-setup-linux-amd64` o su variante arm64.
El script usa `bin/`; si falta el binario, intenta compilar con Go 1.26+.
Tras editar código o payload debes recompilar: no detecta binarios obsoletos.

```sh
./install.sh --list
./install.sh --modules base,profiles,agents,prewalk --dry-run
./install.sh --modules base,profiles,agents,prewalk --yes --install-deps
./install.sh --modules base,profiles,agents,prewalk --check
```

`--yes` autoriza configuración, no dependencias. `--install-deps` autoriza estas
por separado y requiere `--yes` y `--modules`; no admite `--dry-run` o `--check`.
El TUI muestra confirmaciones equivalentes. Sin cuenta ChatGPT verificable no
se aplica configuración. La cuenta y el catálogo se consultan mediante el
app-server local, sin solicitar generaciones. Un proveedor o catálogo externo
configurado bloquea la validación: no se migra silenciosamente.

## Modelos y perfiles

En el TUI, pulsa **m**: ↑/↓ elige rol, ←/→ cambia modelo, Tab cambia esfuerzo,
**d** restaura presets y Enter vuelve a módulos. **c** consulta otra vez la cuenta.
La revisión muestra las elecciones efectivas antes de escribir.

Los presets del proyecto son Astra/medium para el principal, Sol/xhigh para
reviewer, Spark/medium para ejecución/exploración y Luna/medium para respaldos.
Son preferencias del setup, no promesas de disponibilidad. Si falta un preset
ordinario, el instalador propone una alternativa del catálogo. Los respaldos
requieren Luna o una elección explícita disponible. Se rechazan elecciones
explícitas incompatibles. Acceso al catálogo no garantiza cuota libre.

`--models roles.json` permite un objeto parcial, con modelos de tu cuenta:

```json
{"prewalk_executor":{"model":"gpt-5.6-sol","effort":"medium"}}
```

Los roles omitidos siguen la resolución por defecto. En ejecución, el respaldo
configurado sólo se usa ante agotamiento explícito de Spark, no por pruebas
fallidas, red o resultados pobres. No cambia el principal ni consume un reset.
Los perfiles `personal` y `work` heredan configuración compartida: no son
cuentas distintas. No se instalan proveedores externos ni credenciales de API.

## Destinos y recuperación

Se respeta `CODEX_HOME`. `--codex-home` elige un destino explícito; `--home` usa
el hogar indicado e ignora el `CODEX_HOME` heredado salvo override explícito.
Un destino aislado necesita su propio login; no copies credenciales para probar.

Los destinos son el directorio Codex, `~/.local/share/codex-panel`, lanzadores
en `~/.local/bin` y bloques identificados en los archivos del shell. Se conservan
valores ajenos, pero se actualizan los gestionados por el módulo elegido;
el merge TOML puede reformatear o quitar comentarios.

Los originales y `manifest.json` se guardan con permisos privados en
`~/.local/state/codex-setup/backups/<fecha-id>/`. Reinstalar sin cambios no crea
respaldos ni duplica bloques/hooks. No hay desinstalación automática.

Para restaurar, detén el panel. El manifiesto identifica cada destino con `path`
y su original con `file`; `existed=false` indica un archivo nuevo. Restaura sólo
esos destinos, sin borrar carpetas compartidas. Se rechazan symlinks y cambios
posteriores a la vista previa; si falla una escritura se intenta rollback.
Un corte eléctrico o `kill -9` puede requerir recuperación manual.

## Panel

Requiere Linux con `/proc`, terminal interactiva, Python 3.11+ con curses,
sqlite3 y tomllib, tmux real ≥3.3 y `tic` de ncurses. Se recomiendan 120 columnas.
Se ofrecen paquetes con consentimiento separado. Para una ruta especial:
`CODEX_PANEL_TMUX=/ruta/al/tmux ./install.sh`.

Abre una terminal nueva tras instalar:

```sh
codex --profile personal
codex --profile work
codex resume --last
CODEX_PANEL_DISABLE=1 codex     # omitir el panel
```

Bash/Zsh conserva argumentos y no reemplaza el ejecutable de Codex. Los comandos
de servicio (`exec`, `login`, `app-server`, `--help`, etc.) y las ejecuciones sin
TTY pasan al CLI. No añade permisos; si pasas `--yolo`, conserva su significado
de omitir sandbox/aprobaciones. Si `.zshrc` es un symlink gestionado, utiliza
`.zshenv` sin alterar el enlace. Otros shells pueden usar `codex-panel`.

Para desactivar, retira sólo el bloque `codex-setup:launcher` del archivo indicado
en la vista previa o restaura el respaldo. No copies lanzadores entre equipos:
contienen rutas locales; vuelve a instalar allí.

Ctrl-S oculta/muestra el panel. Flechas y Enter abren un agente; Enter cierra
el flotante. La cuota se consulta al abrirlo, no al instalar. Para desactivarla:
`CODEX_PANEL_QUOTA_OFFLINE=1 codex-panel -p personal`. Las estimaciones por agente
no son oficiales. Más opciones en la [guía del panel](../payload/panel/README.md).

## Prewalk, Ponytail y handoff

Prewalk carga su skill para implementaciones no triviales: contratos, worktrees
privados, Qlty y revisión independiente. La [política canónica](../payload/skills/prewalk/SKILL.md)
define el ciclo; los hooks necesitan confianza y no son un sandbox de seguridad.
Las instrucciones no garantizan por sí mismas la corrección del resultado.

Personaliza `$CODEX_HOME/integrations/prewalk/settings.json` y los roles, no los
scripts gestionados. `worktrees`, `max_workers` y `quality` son ajustes del setup.
Los defaults de permisos se añaden sólo si no hay elecciones previas; no se
habilita `--yolo`. Qlty y sus analizadores requieren red para descargarse.
Para apagar Prewalk, retira sólo el bloque `codex-setup:prewalk` de
`developer_instructions` y deshabilita su entrada en `skills.config`.
Preserva las demás instrucciones. Reinstalar lo activa de nuevo.

Ponytail instala seis skills y sus hooks. Usa `$ponytail lite`, `$ponytail full`,
`$ponytail ultra` o `stop ponytail`. Véase su [guía](../payload/integrations/ponytail/README.md).

`context-handoff` avisa por contexto activo y ofrece `$handoff`. Los traspasos son
Markdown privados y acotados en `$CODEX_HOME/handoffs`, sin conversaciones ni
razonamiento oculto. Ajustes: `$CODEX_HOME/integrations/handoff/settings.json`.
La [skill](../payload/handoff.skill.md) define creación, validación y reanudación;
no hace push ni abre otra tarea por sí sola.

## zg opcional

`./install.sh --modules zg --yes --install-deps` instala Node y zg privados,
verifica hashes y dependencias fijadas y configura MCP con rutas absolutas.
No usa npm global ni descarga modelos, arranca el servidor o crea índices.
No ejecutes `zg install`, que administra configuración fuera de este setup.

Sólo se expone `zvec_grep_search`, con `freshness: "wait_for_fresh"` obligatorio.
Úsalo con un índice local pertinente para consultas conceptuales; usa `rg` para
búsquedas exactas, ausencia de índice o errores. Descarta `possibly_stale`,
`timeout` y `error`. Crear índices requiere autorización aparte.
Runtime y reversión del parche: [guía de zg](../payload/integrations/zg/README.md).

## Verificación y límites

Ejecuta `make test check` y `make release` antes de distribuir cambios.
Los tests usan destinos temporales y cuentas simuladas. La suite del panel se
ejecuta en una instalación temporal con servidores tmux propios. Algunos tests
opcionales se omiten si faltan dependencias o fixtures locales.

Se probaron instalaciones limpias amd64 en Ubuntu 24.04 y Debian 12 con cuenta
simulada, y el panel instalado. Se compila arm64, pero no se verificó su runtime
real ni el flujo end-to-end en Fedora/Arch. Confianza de hooks e interacción
humana completa del TUI requieren validación manual. Los formatos internos de
Codex pueden cambiar: revalida nuevas versiones.

## Añadir módulos

El catálogo es [`payload/modules.json`](../payload/modules.json). Añade un ID,
nombre, descripción y operaciones, coloca recursos bajo `payload/` y recompila.
Reutiliza operaciones (`copy`, `tree`, `merge`, `append`, etc.) y pruebas de
[`internal/installer`](../internal/installer). `depends` declara dependencias y
`platforms` limita sistemas. Los destinos son relativos a raíces permitidas;
no se aceptan escapes con `..` ni comandos arbitrarios desde el manifiesto.
No hay un framework de plugins adicional.

## Procedencia

Ponytail conserva su [licencia MIT](../payload/integrations/ponytail/upstream/LICENSE)
y [procedencia fijada](../payload/integrations/ponytail/upstream/PROVENANCE.json).
Go, Bubble Tea y demás dependencias conservan sus propias licencias;
los textos de distribución están en [Third-party notices](THIRD_PARTY_NOTICES.md).
Qlty se descarga del proyecto original; revisa sus términos BSL/Fair Source
antes de ofrecerlo como servicio a terceros. Las referencias de las integraciones
permanecen junto a su código. Las atribuciones de autores de paletas se conservan.

No se ha establecido una licencia global para el código propio. Publicar el
repositorio no añade una licencia sobre sus componentes.
