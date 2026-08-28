#!/usr/bin/env bash
# build-release-webui.sh — build the BUNDLED graphi binary with the web UI
# embedded (SW-066, EP-010 Task C).
#
# The default `go build` is intentionally UI-free (the size-budget gate measures
# that build, and web/dist is not required to compile). This script produces the
# release flavor instead: it builds the Vite web app, copies the output into the
# `go:embed` directory (surfaces/http/webui/dist, which is gitignored — a build
# artifact, never committed), and builds the binary with `-tags webui_embed` so
# the UI is served at "/" over the existing loopback-only HTTP surface.
#
# CGo stays disabled (CGO_ENABLED=0) to preserve the CGo-free invariant.
#
# Usage (from the repo root):
#   scripts/build-release-webui.sh
set -euo pipefail

# Resolve the repo root from this script's location so it works from any cwd.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

EMBED_DIR="surfaces/http/webui/dist"

echo ">> building web UI (npm ci && npm run build)"
(cd web && npm ci && npm run build)

echo ">> copying web/dist -> ${EMBED_DIR} (gitignored embed dir)"
rm -rf "${EMBED_DIR}"
cp -R web/dist "${EMBED_DIR}"

echo ">> building bundled binary with the canonical release builder"
# The canonical builder selects the shipped grammar subset + webui_embed,
# verifies reproducibility, runs the static privacy gate, and embeds the
# source-bound privacy evidence. A direct go build intentionally has no such
# attestation and is therefore not a release artifact.
CGO_ENABLED=0 go run ./cmd/release -webui -version dev -out graphi

echo ">> done: ./graphi (web UI embedded, served at / over loopback)"
