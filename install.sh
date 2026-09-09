#!/bin/sh
set -eu
setup_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
setup_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) setup_arch=amd64 ;;
  aarch64|arm64) setup_arch=arm64 ;;
  *) printf '%s\n' 'Arquitectura no soportada: se requiere Linux amd64/arm64.'; exit 1 ;;
esac
case "$setup_os" in
  linux) ;;
  *) printf '%s\n' 'Sistema no soportado: se requiere Linux amd64/arm64.'; exit 1 ;;
esac
setup_binary="$setup_root/bin/codex-setup-$setup_os-$setup_arch"
if [ ! -x "$setup_binary" ]; then
  if ! command -v go >/dev/null 2>&1; then
    printf '%s\n' 'Falta el binario de esta plataforma y Go 1.26+. Instala Go o compila en otra máquina.'
    exit 1
  fi
  (cd "$setup_root" && go build -trimpath -o "$setup_binary" .)
fi
exec "$setup_binary" "$@"
