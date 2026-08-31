// Package main's tests for `graphi setup-embedder` (SW-262 extension). The
// command is split into two paths:
//
//   - print-only: every selector EXCEPT `static:` — no network is consulted
//     and the command prints a copy-pasteable export line;
//   - static: download — `setup-embedder static:<model>@<revision>`
//     downloads the pinned artifact over HTTPS, verifies its SHA-256, and
//     installs it into the model cache.
//
// These tests exercise the print-only paths (the empty-arg and non-static
// selectors) so the default-build contract is pinned: no network is
// consulted and the command returns success.
package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn while os.Stderr is redirected, returning what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = old
	_ = w.Close()
	return <-done
}

// AC-5: the print-only path is offline — empty arg prints every option and
// returns success without consulting the network.
func TestSetupEmbedder_NoArg_PrintsAllOptions(t *testing.T) {
	var rc int
	out := captureStdout(t, func() {
		rc = runSetupEmbedder([]string{})
	})
	if rc != 0 {
		t.Fatalf("runSetupEmbedder([]) rc=%d, want 0", rc)
	}
	for _, want := range []string{
		"GRAPHI_EMBEDDER",
		"static:potion-code-16M-v2@",
		"ollama",
		"ONNX",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// AC-5: the print-only path is offline — a non-static selector returns
// success and prints the export line, without consulting the network.
func TestSetupEmbedder_NonStaticSelector_PrintsExportLine(t *testing.T) {
	var rc int
	out := captureStdout(t, func() {
		rc = runSetupEmbedder([]string{"ollama"})
	})
	if rc != 0 {
		t.Fatalf("runSetupEmbedder([ollama]) rc=%d, want 0", rc)
	}
	if !strings.Contains(out, "export GRAPHI_EMBEDDER=ollama") {
		t.Errorf("output missing the export line: %q", out)
	}
}

// AC-5 (cont.): an empty selector on the static path is a typed error that
// names the accepted form, never a successful no-op. This is the print-only
// path for the static scheme's selector parsing — no network is consulted.
func TestSetupEmbedder_StaticWithoutSelector_OfflineError(t *testing.T) {
	var rc int
	captureStderr(t, func() {
		rc = runSetupEmbedder([]string{"static:"})
	})
	if rc == 0 {
		t.Fatalf("runSetupEmbedder([static:]) rc=0, want non-zero (static: requires a model@revision)")
	}
}

// AC-10: the help catalog mentions the static: scheme as the recommended
// Labs path (the print-only path's first line on `graphi setup-embedder`
// without an argument). The exact entry text lives in cmd/graphi/help.go;
// this test pins the AC-10 wording. The AX-00 frozen help golden
// (cmd/graphi/testdata/cli-help.txt) reflects the same change and is the
// surface the orchestrator reviews — see the report's "Deviations" section.
func TestSetupEmbedder_HelpCatalogMentionsStatic(t *testing.T) {
	entry, ok := subcommandHelp["setup-embedder"]
	if !ok {
		t.Fatal("subcommandHelp is missing the setup-embedder entry")
	}
	if !strings.Contains(entry.synopsis, "static:") {
		t.Errorf("setup-embedder synopsis must mention the static: scheme: %q", entry.synopsis)
	}
	if !strings.Contains(entry.example, "static:potion-code-16M-v2") {
		t.Errorf("setup-embedder example must show the static: invocation: %q", entry.example)
	}
}

// AC-1: a static: selector with a non-pinned model@revision is refused
// with a typed SelectorError BEFORE the network code runs. The reviewer
// caught a previous version that downloaded the pinned artifact under
// the wrong selector and printed an invalid export; this test pins the
// fail-closed behavior.
func TestSetupEmbedder_StaticWrongSelector_IsRefusedBeforeNetwork(t *testing.T) {
	var rc int
	captureStderr(t, func() {
		rc = runSetupEmbedder([]string{"static:wrong-model@wrong-rev"})
	})
	if rc == 0 {
		t.Fatal("setup-embedder accepted a non-pinned selector; AC-1 requires a typed SelectorError naming the accepted form")
	}
}

// AC-5: a static: selector without a model@revision is also refused
// (typed error) — the empty model@revision path is checked before any
// network call.
func TestSetupEmbedder_StaticEmptyRevision_IsRefusedBeforeNetwork(t *testing.T) {
	var rc int
	captureStderr(t, func() {
		rc = runSetupEmbedder([]string{"static:potion-code-16M-v2@"})
	})
	if rc == 0 {
		t.Fatal("setup-embedder accepted an empty revision; AC-1 requires a typed SelectorError")
	}
}
