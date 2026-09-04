# Codex con panel lateral de subagentes

## Uso

Desde cualquier repositorio, en una terminal normal:

```sh
codex --yolo
codex --profile personal
codex --profile work
# También puedes abrir el panel explícitamente:
codex-panel --profile personal
codex-panel --profile work
```

El instalador integra `codex` mediante una función de Bash/Zsh, sin reemplazar
el ejecutable original. Abre una terminal nueva tras instalar, o recarga el
archivo indicado en la vista previa. Si `.zshrc` es gestionado por Nix, el hook
va en `.zshenv` y solo se aplica a shells interactivos. Para omitir el panel:
`CODEX_PANEL_DISABLE=1 codex` o `command codex`.

La integración conserva argv y perfil por defecto: `codex --yolo` no fuerza
`personal`. `exec`, servicios, ayuda, comandos sin TTY y sesiones remotas pasan
directamente al CLI. `codex resume`/`codex fork` interactivos también pueden usar el panel.

También puedes indicar el proyecto:

```sh
codex-panel --profile personal --cwd ~/projects/tracer
```

Izquierda: Codex CLI original. Derecha: tarjetas interactivas de sus subagentes.
El lanzador no reemplaza el ejecutable de `codex`, no cambia modelos, permisos, perfiles ni el
`tmux` de tu PATH. Si Codex pregunta si confías en el proyecto, responde en el
panel izquierdo; hasta entonces el monitor espera la creación de la sesión.

## Controles

- Haz clic en un panel para enfocarlo, o pulsa `Ctrl-b` y luego una flecha.
- **Ctrl-S**, sin prefijo: oculta/muestra el panel desde Codex o desde el monitor.
  El monitor se mueve a una ventana aparcada, no se termina ni se reinicia.
  Al mostrarlo queda enfocado para navegar. No cambia Ctrl-S fuera de estos dos paneles.
- En el monitor: flechas arriba/abajo o `j`/`k` seleccionan un agente. **Enter** abre
  su actividad en un flotante de tmux, con actualización en vivo.
- En el flotante: flechas desplazan, `End` vuelve al seguimiento automático y
  **solo Enter cierra el flotante**. `Esc` y `q` se ignoran en esa vista.
  No envía mensajes al agente. La espera para distinguir secuencias de teclado
  es de 25 ms y solo afecta al proceso del visor.
- En el monitor, `q`/Esc también lo ocultan; no lo eliminan.
- `Ctrl-b`, luego `z`: amplía/restaura el panel enfocado.
- `Ctrl-b`, luego `d`: desconecta la vista sin parar Codex.
- Sal de Codex normalmente. Para cerrar también el monitor usa `Ctrl-b`, luego `&`
  y confirma el cierre de la ventana. Si estaba oculto, abre/cierra la ventana
  aparcada con los controles normales de tmux. No cierres toda la sesión mientras
  quieras mantener trabajando a Codex.
- Usa `/agent` dentro de Codex para controlar o inspeccionar un subagente. El monitor no lo controla.

Para reconectar una vista desconectada:

```sh
tmux -L NOMBRE attach -t NOMBRE
```

Opciones adicionales de Codex van después de `--`:

```sh
codex-panel -p personal -- --sandbox read-only
codex-panel -p work -- resume ID_DE_SESION
```

Al desconectar se imprime el comando de reconexión con la ruta de tmux y el nombre
exactos. Usa esa ruta si `tmux` en tu PATH es un shim.

`--width 48` establece el ancho máximo del panel derecho; se reduce en terminales
estrechas. Se recomienda una terminal de al menos 120 columnas. Ejecuta el
lanzador desde una terminal sin otro tmux para evitar prefijos de teclado anidados.

## Tarjetas y métricas

Cada tarjeta muestra apodo/tarea, modelo/esfuerzo, `Running` o `Done` (con estados
distintos para error, interrupción o falta de datos), duración acumulada de los
turnos y tokens. Las tarjetas ya no muestran flechas de comunicación; el registro
original sigue disponible en el flotante. El registro puede estar retrasado
respecto del modelo.

La vista principal no imprime párrafos de lo que hace cada agente. El flotante
muestra los registros públicos originales: llamadas a herramientas con sus entradas,
comandos, salidas y mensajes del agente, separados por evento/hora e identificador
de llamada. No traduce, resume ni sustituye los registros por frases del monitor.
Conserva saltos de línea e indentación y ajusta las líneas al ancho sin cortarlas.
`Home` lleva al principio, `End` retoma el seguimiento, `PgUp/PgDown` desplazan.
Los mensajes conservan el idioma original del agente. Una salida que Codex ya
guardó truncada no puede reconstruirse; se muestra tal como está registrada.
El historial visible tiene un límite de 8 MiB de texto/índices y 4096 eventos por
agente. Si se alcanza, aparece un aviso explícito `[OMITTED: …]`; los JSONL
originales no se modifican. El monitor no genera resúmenes con modelos.

**Tokens:** total reportado de entrada + salida. Los tokens de entrada en caché
ya están incluidos en entrada, y los de razonamiento en salida: no se suman dos veces.
Se ignora el historial heredado del padre y se deduplican snapshots de uso y rutas.

**Tokens:** las tarjetas muestran el contador completo, sin abreviarlo. En el
flotante, `Token share` sigue siendo tokens del agente / tokens de Main y todos
los subagentes directos. No se muestra si falta algún contador necesario.

**Est. quota share ~X% of session:** reparto aproximado del consumo de la sesión,
ponderado por tarifas públicas de créditos de Codex. No es el porcentaje de toda
la cuenta ni una medición oficial. El denominador incluye Main y todos los agentes.

```
peso = (entrada - caché) × tarifa_entrada
     + caché × tarifa_caché + salida × tarifa_salida
reparto estimado = 100 × peso_agente / suma_pesos_de_la_sesión
```

Se usan las tarifas de créditos verificadas el 2026-09-03 (no precios de la API).
Sol: 100/10/500; Terra: 50/5/300; Luna: 5/0.5/30 créditos por millón de tokens,
respectivamente entrada/caché/salida. El módulo incluye también 5.5/5.4/5.4-mini.
Si el registro no informa velocidad, se asume Standard y se indica en el flotante;
Fast explícito aplica el multiplicador publicado. No se añade razonamiento otra
vez porque ya está incluido en salida. Si faltan contadores, cambia el modelo/tier
sin desglose por modelo o hay una tarifa desconocida, se muestra `—` sin excluir
silenciosamente a ese miembro del denominador. La tabla exige revalidación tras
el 2026-11-21; no se descargan tarifas automáticamente.

**ACCOUNT QUOTA:** sustituye el antiguo aviso. Barras de porcentaje **usado** para
las ventanas primaria/secundaria de la cuenta, con tiempo hasta reinicio y edad
de la última lectura. Se consulta cada 30 segundos, sin bloquear el teclado,
mediante `account/rateLimits/read` del app-server oficial por stdio. No se abre
ninguna conversación ni se llama a un modelo. La cuota incluye el resto de tus
sesiones/dispositivos; por eso no se multiplica por el reparto de esta sesión.
Ante errores conserva el último valor marcado `STALE`; valores desconocidos o
ventanas ya vencidas no se convierten en 0%. No compra créditos ni consume resets.

La pantalla se actualiza cada segundo. `Running` significa inicio de turno sin fin
registrado. Las salidas aparecen cuando Codex las escribe en el JSONL; si una
herramienta solo registra su resultado al finalizar, no hay salida intermedia que
mostrar. No muestra razonamiento interno, instrucciones del sistema ni cuerpos
cifrados. Se eliminan controles de terminal y se ocultan credenciales reconocibles
con `[REDACTED]`, sin ocultar etiquetas como `Token share`. La detección de secretos
es de mejor esfuerzo: trata el flotante como información privada del proyecto.

No consume tokens de modelos. Lee los JSONL de Codex de forma incremental y
consulta su índice SQLite con `mode=ro`, sin reanudar conversaciones. Solo el
medidor de cuenta hace consultas de red autenticadas a través de Codex, usando
el mismo CODEX_HOME de la cuenta (compartido por personal/work); no lee ni imprime
credenciales directamente. `app-server` no admite `--profile` en esta versión. Los
flotantes y `--once` no consultan la cuenta. Para desactivar esas consultas:
`CODEX_PANEL_QUOTA_OFFLINE=1 codex-panel -p personal`.
La identidad viene de los registros abiertos por el proceso que lanzó esa ventana,
no de escoger la última conversación del directorio. Cada ventana queda separada.
Los subagentes se filtran por el ID exacto de su padre (la configuración actual
solo permite un nivel). `/new` cambia el monitor a la nueva raíz detectada.

Si Codex se cierra sin registrar el fin, se muestra un estado sin confirmar y se
congela el tiempo observado. La interfaz no asume que la ausencia de eventos es un bloqueo.

## Idioma y colores

Codex CLI 0.153.0 usa UI inglesa; el panel usa inglés también, aunque los mensajes
de los agentes conservan su idioma original. No confunde el idioma de las respuestas
con el de la interfaz ni usa el idioma de la app de escritorio. Para una preferencia
manual del panel: `CODEX_PANEL_LANG=es codex-panel -p personal`.

Se relee `tui.theme` de `~/.codex/config.toml` y del perfil cada segundo. Al guardar
un cambio con `/theme`, el panel y el flotante refrescan sus colores. Una previsualización
no guardada del selector no es detectable externamente. Los temas `.tmTheme` propios
se leen desde `$CODEX_HOME/themes`. Como en Codex, **el fondo y texto normales son
los predeterminados del terminal**, no el fondo/foreground del editor de sintaxis.
Los acentos siguen el tema guardado; la selección usa vídeo inverso y el texto
secundario usa dim. Así un tema de sintaxis no crea un bloque de fondo distinto.
Incluye las paletas verificadas de los 32 temas de esta versión del CLI, además de
los temas personalizados. Una definición terminfo local permite combinar RGB
directo y los índices exactos de los temas ANSI/base16 sin modificar tu terminal
ni el entorno del Codex principal. En otros terminales se usa una aproximación
de 256 colores si no hay color directo.
Un tema desconocido usa colores del terminal y se identifica como fallback.
Los estilos `window-style` y `window-active-style` se establecen solo en el pane
del monitor (`set-option -p`); la barra de estado solo en su sesión. El flotante
recibe estilos propios al abrirse. No se modifica `.tmux.conf`, la paleta global
del emulador, el pane principal ni los estilos de otras sesiones.
Los overrides de tema mediante argumentos `-c`, configuración de proyecto o un
host remoto no se detectan: se sigue la selección guardada global/del perfil.

Referencias: [temas de sintaxis en Codex](https://learn.chatgpt.com/docs/cli-customization)
y [opciones por pane/sesión de tmux](https://man.openbsd.org/tmux).
Métricas: [tarifas de créditos](https://learn.chatgpt.com/docs/pricing),
[Fast mode](https://learn.chatgpt.com/docs/agent-configuration/speed) y
[cuota de cuenta](https://learn.chatgpt.com/docs/app-server).

## Compatibilidad y dependencias

Validado con Linux, Codex CLI 0.153.0, Python 3 y tmux 3.7b. Solo usa la biblioteca
estándar de Python. No funciona con sesiones `--ephemeral`, hosts `--remote` o
procesos inaccesibles mediante `/proc`. Los formatos locales de Codex son internos:
una actualización puede requerir adaptar el lector. No es un plugin oficial.

El instalador portable detecta un tmux real en el equipo destino y genera el
lanzador con esa ruta. No copia binarios Nix ni utiliza/reemplaza shims de tmux.
Puedes indicar una ruta con `CODEX_PANEL_TMUX`. Requiere Python 3.11+ con curses
de colores extendidos y `tic`, que compila terminfo para el equipo destino.
Cada lanzamiento usa un servidor tmux separado con un nombre único, sin cargar tu
`.tmux.conf`, sin heredar el entorno de otro lanzamiento y sin alterar tus sesiones existentes.

## Diagnóstico sin iniciar modelos

```sh
codex-panel watch --thread ID_DE_SESION --once
codex-panel watch --pid PID_DE_CODEX --once
cd ~/.local/share/codex-panel
python3 -m unittest discover -v
```

Un proceso lanzador conserva temporalmente su PID y control de paneles en un directorio
privado de `XDG_RUNTIME_DIR` (o `/tmp`). Los argumentos de lanzamiento se eliminan al
iniciar Codex. El directorio de control se conserva al ocultar el panel; puede permanecer
hasta reiniciar después de cerrar tmux. No contiene transcripciones ni credenciales.

Para desinstalar, retira `~/.local/bin/codex-panel` y la carpeta
`~/.local/share/codex-panel` después de cerrar sus sesiones. La configuración
original de Codex y los perfiles no necesitan restauración.
