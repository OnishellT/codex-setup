# zg MCP opcional

Este módulo opcional instala una entrada MCP de `zg` con `enabled = true`,
compartida por work y personal. Antes de escribir, el instalador exige Node.js
22 o superior, `@zvec/zvec-grep@0.2.1` global y que `zg` en PATH corresponda a ese
paquete. En Linux también verifica el parche PR 86 y su backup. Si falta algo,
aborta con un error accionable; no deja una entrada desactivada. El setup no
descarga paquetes, binarios, modelos ni índices, y no inicia el servidor.

En Linux, aplica y verifica primero el parche PR 86. El issue `#92` se mitiga
operativamente con freshness por búsqueda, no confiando en indexación en segundo
plano ni añadiendo un daemon alternativo. Las instalaciones nuevas y las
reinstalaciones quedan habilitadas después de superar la comprobación.

Observación histórica del 2026-09-04 en Linux, Node 24.19.0 y zg 0.2.1 con este
parche: 512 archivos ignorados no añadieron vigilancias (2 antes y 2 después),
pero después de 31 minutos sin consultas las vigilancias desaparecieron y una
edición no se indexó sola (#92). `zg query --refresh wait` recuperó el resultado.
No deduzcas de esa observación el estado actual del MCP: la mitigación es pedir
freshness en cada búsqueda.

Como preparación manual, este módulo incluye `pr86-watch-manager.mjs`, un parche
reversible para el cambio upstream [PR 86](https://github.com/zvec-ai/zvec-grep/pull/86).
El instalador reutiliza su verificación de sólo lectura, pero nunca aplica ni
revierte el parche; tampoco se ejecuta al iniciar MCP. En Linux, después de
instalar el paquete y antes de instalar el módulo, revísalo y aplícalo desde
la carpeta del setup:

```sh
node payload/integrations/zg/pr86-watch-manager.mjs apply
node payload/integrations/zg/pr86-watch-manager.mjs check
./install.sh --modules zg --yes
```

El helper sólo acepta `@zvec/zvec-grep@0.2.1` y los hashes exactos del archivo
original y parcheado; rechaza versiones, contenido o backups desconocidos. Crea
una sola copia `watch-manager.js.codex-setup-pr86.original` junto al archivo
upstream y sólo restaura desde esa copia si conserva el hash original:

```sh
node "${CODEX_HOME:-$HOME/.codex}/integrations/zg/pr86-watch-manager.mjs" restore
```

No lo apliques en macOS. `apply` exige Linux y usa `npm root -g`; la opción
`--package-root RUTA` está reservada para pruebas con un paquete fixture.
Antes de aplicar o revertir, detén el daemon correspondiente con `zg server off`:
un proceso ya iniciado mantiene el código anterior en memoria. Esto afecta a sus
otras sesiones. Al reinstalar/actualizar npm el parche puede desaparecer; vuelve
a comprobarlo, sin forzar el helper sobre otra versión. Es un parche local del
archivo compilado, no una nueva versión oficial, y no corrige el issue #92.
Si ya instalaste el módulo, también puedes usar la copia del helper en
`$CODEX_HOME/integrations/zg/`. Para una primera instalación usando sólo el binario,
lleva esa carpeta del payload si necesitas aplicar el parche. La comprobación
se hace en la máquina del instalador y no se repite en cada arranque de Codex;
revalida después de actualizar dependencias. macOS sigue sin validación real.

La versión prevista es `@zvec/zvec-grep@0.2.1`. Instálala manualmente:

```sh
npm install -g @zvec/zvec-grep@0.2.1
# Sólo si sharp detecta libvips global y esa instalación falla:
SHARP_IGNORE_GLOBAL_LIBVIPS=1 npm install -g @zvec/zvec-grep@0.2.1
```

Crear un índice es una acción explícita del usuario y debe limitarse al
repositorio elegido, nunca a `$HOME`:

```sh
zg index /ruta/repo --embedding local/potion-code-16m-v2 --device cpu
```

No ejecutes `zg install`: ese comando gestiona su propia configuración, AGENTS y
aprobaciones fuera del control de este setup.

Al activarlo, el transporte MCP
stdio ejecuta `zg server --stdio --mcp-toolset agent`; el servidor inicia su
daemon. Sólo se expone `zvec_grep_search`, con aprobación `auto`, tiempo de inicio
de 30 segundos, tiempo de herramienta de 120 segundos y `required = false`.
`auto` no significa aprobación automática: en Codex CLI 0.153.2 una sesión con
`approval_policy = "never"` puede rechazar esta herramienta. En ese caso se
mantiene el fallback a `rg`; el setup no autoriza globalmente las llamadas.

Usa un índice existente sólo para el repositorio explícitamente elegido, nunca
para `$HOME`; el modelo local previsto para ese índice es `potion-code-16m-v2`.
Que el índice sea local no implica resultados ocultos de un proveedor. Si se
expone por loopback, su token opcional se gestiona fuera de este setup: aquí no se
guarda ninguna ruta, variable de entorno ni secreto.

Usa zg únicamente si hay un índice pertinente **y** la búsqueda es conceptual o
la ubicación es desconocida. Para una coincidencia exacta o una revisión
exhaustiva usa `rg` y verifica el archivo actual. Si zg falta, falla o no hay
índice, vuelve a `rg`. No crees, reconstruyas ni borres índices, ni autorices
acceso remoto, sin petición explícita del usuario.

El orquestador y cada subagente deben enviar `freshness: "wait_for_fresh"` en
**cada** `zvec_grep_search`. Es una guía para agentes, no una opción por defecto
del servidor MCP. Ante `possibly_stale`, `timeout` o `error`, descarta el
resultado, usa `rg` y comprueba el archivo vivo. No uses la indexación en segundo
plano como garantía de freshness; esta es la mitigación de #92 sólo por búsqueda.

Para desactivarlo en sesiones futuras, deja `enabled = false` en
`[mcp_servers.zvec_grep]` y detén el daemon compartido ahora con:

```sh
zg server off
```

`zg server off` afecta a las demás sesiones o herramientas que usen ese daemon,
pero no borra índices. Reaplicar el módulo vuelve a establecer `enabled = true`
si los requisitos pasan; no cambia la aprobación `auto` ni autoriza herramientas.
