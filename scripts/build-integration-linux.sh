#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

if [[ $# -gt 1 ]]; then
  echo "Uso: $0 [directorio-de-salida]" >&2
  exit 2
fi
if [[ ! -s web/dist/index.html ]]; then
  echo "Falta web/dist/index.html; ejecutá bun install --frozen-lockfile y bun run build en web/." >&2
  exit 1
fi

version=$(tr -d '[:space:]' < VERSION)
if [[ ! "$version" =~ ^[0-9A-Za-z.+_-]+$ ]]; then
  echo "VERSION debe contener una versión válida, sin espacios." >&2
  exit 1
fi

output=${1:-dist/integration-linux}
mkdir -p -- "$output"
output=$(cd -- "$output" && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

# No se incrustan rutas locales, estado de Git ni marcas de tiempo del runner.
export CGO_ENABLED=0 GOOS=linux GOAMD64=v1 GOARM64=v8.0
ldflags="-s -w -buildid= -X 9router/proxy/internal/updater.CurrentVersion=$version"
for arch in amd64 arm64; do
  name="capimux-linux-$arch"
  for pass in 1 2; do
    GOARCH="$arch" go build -mod=readonly -trimpath -buildvcs=false \
      -ldflags="$ldflags" -o "$work/$name.$pass" ./cmd/9router-go/
  done
  cmp "$work/$name.1" "$work/$name.2"
  mv -- "$work/$name.1" "$work/$name"
done

(
  cd "$work"
  sha256sum capimux-linux-amd64 capimux-linux-arm64 > SHA256SUMS.txt
  sha256sum -c SHA256SUMS.txt
)
for name in capimux-linux-amd64 capimux-linux-arm64 SHA256SUMS.txt; do
  mv -- "$work/$name" "$output/$name"
done
printf 'Binarios Linux reproducibles y checksums: %s\n' "$output"
