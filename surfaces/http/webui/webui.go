// Package webui holds the (optionally embedded) graphi web UI served by the
// loopback-only surfaces/http surface at "/". The embed is gated behind the
// `webui_embed` build tag (embed_on.go) so the DEFAULT build is UI-free: no
// web/dist is required to compile, and the size-budget gate measures a UI-free
// binary. Without the tag, FS stays nil and Enabled() is false, so the server
// serves a small notice page instead of the SPA.
//
// Layering: surfaces. It imports only the standard library (io/fs, embed). It is
// consumed sideways by surfaces/http (same layer) and never reaches up into cmd.
//
// Determinism + local-first: FS, when populated, is a read-only embedded
// filesystem of static build artifacts only — no wall-clock, no rand, no
// outbound network. Serving it changes nothing about the loopback-only contract.
package webui

import "io/fs"

// FS is the embedded web UI filesystem rooted at the dist directory contents
// (so "index.html" and "assets/..." resolve directly). It is nil in the default
// (UI-free) build and is populated by embed_on.go's init under -tags webui_embed.
var FS fs.FS

// Enabled reports whether the web UI was embedded in this binary (i.e. it was
// built with -tags webui_embed and web/dist was present at build time).
func Enabled() bool { return FS != nil }

// NoticeHTML is the minimal, dependency-free page served at "/" when the UI was
// not embedded. It tells the operator how to obtain a bundled binary or build
// one with the embed tag. It is intentionally a single self-contained string so
// the default build needs no template/asset of any kind.
const NoticeHTML = "<!doctype html><meta charset=utf-8><title>graphi</title><body style=\"font-family:system-ui;max-width:40rem;margin:4rem auto\"><h1>graphi UI not bundled</h1><p>This binary was built without the embedded web UI. Install the bundled release (<code>curl -fsSL https://raw.githubusercontent.com/samibel/graphi/main/install.sh | sh</code>) or build with <code>-tags webui_embed</code> after <code>cd web &amp;&amp; npm ci &amp;&amp; npm run build</code>.</p>"
