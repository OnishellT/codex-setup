# zg MCP opcional

Selecciona zg en el menú o ejecuta:

```sh
./install.sh --modules zg --yes --install-deps
./install.sh --modules zg --check
```

El instalador prepara un runtime privado bajo `CODEX_HOME/integrations/zg`:
Node **22.23.2** en `node-v22.23.2` y **@zvec/zvec-grep@0.2.1** en
`packages-v0.2.1`. Verifica los hashes del archivo Node y del paquete principal,
usa npm privado con `--ignore-scripts`, sin configuración ni instalación global,
y verifica las dependencias nativas. Aplica PR 86 únicamente en su paquete
privado de staging y comprueba el parche/backup antes de exponer el directorio.

No modifica paquetes globales, inicia el servidor durante la instalación,
descarga modelos ni crea, reconstruye o borra índices. Si un directorio privado
existente no es válido, lo conserva y falla con diagnóstico: no lo reemplaza
silenciosamente. Los fallos pueden dejar Node ya instalado; volver a intentar
reutiliza ese runtime después de validarlo.

La entrada MCP usa rutas absolutas al Node y al entrypoint privados, con
`server --stdio --mcp-toolset agent`. Queda habilitada para work/personal,
con `required = false`, sólo `zvec_grep_search`, aprobación `auto`, inicio de
30 segundos y herramienta de 120 segundos. El servidor arranca cuando Codex
lo carga, no cuando el instalador hace su comprobación. Las aprobaciones nativas
siguen aplicando; si una llamada es rechazada se vuelve a `rg`, sin conceder
permisos globalmente.

## Parche Linux

El helper [PR 86](https://github.com/zvec-ai/zvec-grep/pull/86) sólo admite la
versión 0.2.1 y hashes conocidos. Conserva una única copia original
`watch-manager.js.codex-setup-pr86.original` y rechaza archivos o backups
desconocidos. No corrige el issue #92.

Para comprobarlo manualmente, usa siempre la raíz del paquete privado:

```sh
zg_root="${CODEX_HOME:-$HOME/.codex}/integrations/zg"
zg_node="$zg_root/node-v22.23.2/bin/node"
zg_package="$zg_root/packages-v0.2.1/node_modules/@zvec/zvec-grep"
"$zg_node" "$zg_root/pr86-watch-manager.mjs" check --package-root "$zg_package"
```

Para una reversión explícita, detén antes su daemon y luego usa
`restore --package-root "$zg_package"` en el mismo helper. Detener el daemon
afecta a otras sesiones; los procesos ya iniciados conservan el código anterior.
Tras restaurar el original, `--check` rechaza la instalación hasta aplicar y
verificar de nuevo el parche conocido. No fuerces el helper sobre otra versión.

Observación histórica del 2026-09-04 en Linux, Node 24.19.0 y zg 0.2.1: con
PR 86, archivos ignorados no añadieron vigilancias; después de 31 minutos sin
consultas las vigilancias desaparecieron y una edición no se indexó sola (#92).
`zg query --refresh wait` recuperó el resultado. No uses esa observación como
garantía del estado actual del MCP.

## Índices y uso

Usa zg sólo si existe un índice pertinente y la búsqueda es conceptual o la
ubicación es desconocida. Para coincidencias exactas/exhaustivas, ausencia de
índice o fallos, usa `rg` y verifica el archivo vivo. No autorices índices remotos
ni crees/reconstruyas/borres índices sin petición explícita.

El orquestador y **cada subagente** deben enviar
`freshness: "wait_for_fresh"` en cada `zvec_grep_search`. No es un valor por
defecto del servidor. Descarta `possibly_stale`, `timeout` y `error`; vuelve
a `rg`. La indexación en segundo plano no garantiza freshness.

Sólo si el usuario solicita crear un índice, limítalo al repositorio elegido,
nunca a HOME. Usando las variables del ejemplo anterior:

```sh
"$zg_node" "$zg_package/dist/cli/index.js" index /ruta/repo --embedding local/potion-code-16m-v2 --device cpu
```

Este comando puede descargar el modelo y crear datos; no forma parte del
onboarding. No ejecutes `zg install`: administra configuración y aprobaciones
fuera de este setup. No se copian secretos ni tokens de servicios loopback.

Para desactivar MCP en sesiones futuras, cambia `enabled = false` en
`[mcp_servers.zvec_grep]`. Para detener explícitamente el daemon compartido:

```sh
"$zg_node" "$zg_package/dist/cli/index.js" server off
```

Afecta a las sesiones que lo usan, pero no borra índices. Reinstalar el módulo
vuelve a habilitarlo después de comprobar sus requisitos.
