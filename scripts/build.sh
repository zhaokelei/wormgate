#!/usr/bin/env bash
# Build fps (server) and fpc (client) for every supported platform.
#
#   ./scripts/build.sh              # build all targets
#   SKIP_WEB=1 ./scripts/build.sh   # reuse the already embedded web/dist
#   OUT=release ./scripts/build.sh  # change the output directory
#
# The front-end bundles are compiled into both binaries by go:embed, so a
# change to web/ requires a Vite rebuild BEFORE the Go cross-compilation.
# CGO_ENABLED=0 keeps every artifact a single static file with no libc
# dependency - important for scratch containers and old macOS/Linux hosts.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
OUT=${OUT:-dist}
GO=${GO:-go}
mkdir -p "$OUT"

if [[ "${SKIP_WEB:-0}" != "1" ]]; then
  if [[ -d web/node_modules ]]; then
    echo "==> rebuilding web consoles"
    (cd web && npm run build)
  else
    echo "==> web/node_modules missing; reusing embedded dist" >&2
  fi
fi

export CGO_ENABLED=0
LDFLAGS="-s -w -buildid="

# os arch  -- Linux + macOS are the primary targets; windows is kept so the
# project can still be developed/tested on the maintainer's machine.
TARGETS=(
  "linux   amd64"
  "linux   arm64"
  "darwin  amd64"
  "darwin  arm64"
  "windows amd64"
)

count=0
for target in "${TARGETS[@]}"; do
  read -r os arch <<<"$target"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  for bin in fps fpc; do
    out="$OUT/${bin}-${os}-${arch}${ext}"
    echo "==> $bin $os/$arch -> $out"
    GOOS="$os" GOARCH="$arch" "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$out" "./cmd/$bin"
    count=$((count + 1))
  done
done

echo "==> $count binaries in $OUT/"
ls -lh "$OUT" | tail -n +2

# Cheap format sanity check without relying on a `file` binary being present:
# ELF starts with 7f 45 4c 46, Mach-O with cf fa ed fe (64-bit LE), PE with MZ.
echo "==> verifying binary headers"
for f in "$OUT"/*; do
  magic=$(od -An -tx1 -N4 "$f" | tr -d ' \n')
  case "$magic" in
    7f454c46) kind=ELF ;;
    cffaedfe | feedfacf) kind=Mach-O ;;
    4d5a*) kind=PE ;;
    *) echo "    UNKNOWN magic $magic for $f" >&2; exit 1 ;;
  esac
  printf '    %-34s %s\n' "$(basename "$f")" "$kind"
done
echo "==> done"
