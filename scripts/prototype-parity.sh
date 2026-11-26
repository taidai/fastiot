#!/usr/bin/env bash
# One-command prototype-parity guard: build the embedded H5, build cmd/pumphouse
# in Docker, seed the teaching project on a real backend, then run the
# Playwright assertions in
# frontend-uniapp/src/pages/overview/prototype-parity.real-browser.mjs.
#
# The host has no Go toolchain, so the binary is built in golang:1.27.3. The
# module cache lives on a named volume; the source stays bind-mounted.
# Prerequisites: `npm ci` in frontend-uniapp (for build:h5 and playwright-core)
# and a system Chrome or Edge for playwright-core to launch.
# Usage: scripts/prototype-parity.sh [http-port]
set -euo pipefail
# Git Bash rewrites container paths passed to -v/-w without this.
export MSYS_NO_PATHCONV=1

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# npm is a native Windows binary: it needs the drive form, while Docker under
# MSYS_NO_PATHCONV keeps the /c/... form.
WROOT="$(cygpath -w "$ROOT")"
PORT="${1:-${FASTEMS_PARITY_PORT:-18405}}"
PASSWORD="${FASTEMS_PARITY_PASSWORD:-parity-demo-2026}"
IMAGE="${FASTEMS_GO_IMAGE:-golang:1.27.3}"
MODVOL="ticket05-gomod"
BINVOL="ticket05-pumphouse-bin"
NAME="ticket05-parity-$PORT"

echo "== build H5 (embedded resources) =="
npm --prefix "$WROOT\\frontend-uniapp" run build:h5

echo "== build cmd/pumphouse in $IMAGE =="
docker volume create "$MODVOL" >/dev/null
docker volume create "$BINVOL" >/dev/null
docker run --rm \
  -v "$ROOT":/src -v "$MODVOL":/go/pkg/mod -v "$BINVOL":/out -w /src \
  -e CGO_ENABLED=0 "$IMAGE" go build -trimpath -o /out/pumphouse ./cmd/pumphouse

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== start the seeded pumphouse backend on 127.0.0.1:$PORT =="
docker run -d --name "$NAME" \
  -p "127.0.0.1:$PORT:8080" \
  -v "$BINVOL":/bin \
  -e FASTEMS_ADMIN_PASSWORD="$PASSWORD" \
  "$IMAGE" /bin/pumphouse -project -port 8080 -nats-port 4222 -data /data >/dev/null

ready=0
for _ in $(seq 1 90); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/health" || true)" = "200" ]; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "pumphouse did not become ready on port $PORT" >&2
  docker logs "$NAME" 2>&1 | tail -30 >&2 || true
  exit 1
fi

echo "== prototype parity assertions =="
cd "$ROOT/frontend-uniapp"
PARITY_ORIGIN="http://127.0.0.1:$PORT" PARITY_PASSWORD="$PASSWORD" \
  node src/pages/overview/prototype-parity.real-browser.mjs
