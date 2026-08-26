package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/internal/goldenfile"
)

// AX-00 (SW-220) AC-2, CLI half — the user-facing verb help freeze.
//
// The CLI is the second surface the operation catalog will project onto
// (AX-05), and CLI help is where the Stable/Labs tier is rendered for a human:
// `stabilityMarker` derives the `[labs] ` prefix from surfaces/mcp.
// StableOperations, so a re-tiering that the coverage matrix somehow let past
// would still change this text. Freezing the rendered help therefore pins three
// things at once — the verb SET, each verb's synopsis/usage/example, and its
// TIER MARKER.
//
// The artifact is plain text, not JSON, on purpose: this is exactly what a user
// sees, and a diff of it is readable without decoding.
//
// Regeneration is explicit:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./cmd/graphi -run TestAX00

// renderCLIHelp produces the whole frozen help artifact: the top-level help
// blurb, the "unknown subcommand" listing (which is where every verb's tier
// marker is rendered side by side), and the per-verb help for every long verb
// and every short alias.
func renderCLIHelp(t *testing.T) []byte {
	t.Helper()

	var out bytes.Buffer
	out.WriteString("# AX-00 baseline freeze — graphi CLI help\n")
	out.WriteString("#\n")
	out.WriteString("# Frozen rendering of the user-facing CLI verbs: the top-level blurb, the\n")
	out.WriteString("# subcommand listing with its Stable/Labs markers, and every verb's own help.\n")
	out.WriteString("# Tier markers are DERIVED (cmd/graphi/help.go stabilityMarker →\n")
	out.WriteString("# surfaces/mcp.IsStableOperation), so a re-tiering shows up here as a diff.\n")
	out.WriteString("# Regenerate deliberately: GRAPHI_UPDATE_GOLDEN=1 go test ./cmd/graphi -run TestAX00\n")

	out.WriteString("\n\n===== graphi help =====\n")
	out.WriteString(captureStdout(t, func() { printHelp() }))

	out.WriteString("\n===== graphi help <unknown> (the verb listing with tier markers) =====\n")
	var listing bytes.Buffer
	code := runHelp([]string{"__no_such_subcommand__"}, &listing)
	fmt.Fprintf(&out, "exit code: %d\n", code)
	out.Write(listing.Bytes())

	out.WriteString("\n===== per-verb help =====\n")
	for _, name := range helpVerbNames() {
		var buf bytes.Buffer
		if !printSubcommandHelp(name, &buf) {
			t.Fatalf("verb %q has no help entry — a user-facing verb must not ship help-less", name)
		}
		fmt.Fprintf(&out, "\n--- graphi help %s ---\n", name)
		out.Write(buf.Bytes())
	}
	return out.Bytes()
}

// helpVerbNames is the sorted, deduplicated set of every verb the help system
// answers for: the long subcommands plus the short query/analyze aliases. It is
// derived from the production tables, never hand-listed, so a verb added without
// a help entry fails renderCLIHelp above rather than silently escaping the
// golden.
func helpVerbNames() []string {
	seen := map[string]bool{}
	for name := range subcommandHelp {
		seen[name] = true
	}
	for name := range queryVerbSet {
		seen[name] = true
	}
	for name := range analyzeVerbSet() {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestAX00_CLIHelp_Golden(t *testing.T) {
	goldenfile.Assert(t, filepath.Join("testdata", "cli-help.txt"), renderCLIHelp(t))
}

// TestAX00_CLIHelp_ReproducibleAcrossRuns is the AC-2 determinism half. The help
// text interpolates parse.NewDefaultRegistry().Languages() and walks several Go
// maps, so "two consecutive runs are byte-identical" is a real assertion here,
// not a formality.
func TestAX00_CLIHelp_ReproducibleAcrossRuns(t *testing.T) {
	first := renderCLIHelp(t)
	second := renderCLIHelp(t)
	if !bytes.Equal(first, second) {
		t.Errorf("CLI help is not reproducible across two consecutive runs (map iteration order in the registry or the verb tables?):\n first =%s\n second=%s", first, second)
	}
}
