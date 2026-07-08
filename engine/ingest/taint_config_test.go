package ingest_test

// WP-09 integration gate. Proves the per-project taint config
// (<root>/.graphi/taint.json) actually shapes the findings the ingest pipeline
// persists — the real surfaced path `graphi analyze taint` reads. The fixture
// flows attacker-controlled input into render.HTML, a sink the built-in default
// config does NOT know. Without a config file the pipeline persists no finding
// for it (byte-parity with pre-WP-09 behaviour); with a config file that adds
// render.HTML as a custom XSS sink, the same source produces a persisted
// finding. The two directions together show the loader is wired in AND that its
// absence changes nothing.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/ingest"
)

// customSinkSource is a handler where a tainted query param reaches render.HTML,
// a call the default config classifies as neither source nor sink.
const customSinkSource = `package app

import (
	"net/http"

	"github.com/acme/render"
)

// Show renders a user-controlled name. render.HTML is only a sink under a
// project config that declares it; the default config leaves it unclassified.
func Show(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	render.HTML(w, name)
}
`

// customSinkConfig adds render.HTML as an XSS sink on top of the defaults.
const customSinkConfig = `{
	"sinks": [
		{"id": "render_html", "category": "xss", "name_patterns": ["render.HTML"]}
	]
}`

func customSinkRepo(withConfig bool) map[string]string {
	files := map[string]string{
		"go.mod":      "module app\n\ngo 1.21\n",
		"handlers.go": customSinkSource,
	}
	if withConfig {
		files[".graphi/taint.json"] = customSinkConfig
	}
	return files
}

// countXSS returns the number of persisted findings whose provenance touches the
// Show handler and carries the custom XSS category.
func countXSS(findings []taint.Finding) int {
	n := 0
	for _, f := range findings {
		if f.SinkCategory == "xss" && pathTouches(f, "Show") {
			n++
		}
	}
	return n
}

func ingestAndReadTaint(t *testing.T, withConfig bool) []taint.Finding {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	repo := writeRepo(t, customSinkRepo(withConfig))
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest custom-sink fixture (withConfig=%v): %v", withConfig, err)
	}
	findings, err := ing.IntraProcTaintFindings(ctx)
	if err != nil {
		t.Fatalf("read intra-proc taint findings: %v", err)
	}
	return findings
}

// TestTaintConfig_CustomSinkChangesFindings is the WP-09 acceptance gate: the
// custom sink is invisible to the default config and visible once the project
// config declares it, both measured off the persisted findings the ingest
// pipeline computed with LoadConfig(root).
func TestTaintConfig_CustomSinkChangesFindings(t *testing.T) {
	// Control: no .graphi/taint.json — the default config does not know
	// render.HTML, so no XSS finding is persisted (behaviour unchanged from
	// before WP-09).
	if got := countXSS(ingestAndReadTaint(t, false)); got != 0 {
		t.Errorf("default config produced %d render.HTML findings, want 0 (sink not in defaults)", got)
	}

	// Treatment: the project config adds render.HTML as an XSS sink, so the SAME
	// tainted source→sink flow now persists exactly one finding.
	if got := countXSS(ingestAndReadTaint(t, true)); got != 1 {
		t.Errorf("project config produced %d render.HTML findings, want 1 (custom sink not wired into ingest)", got)
	}
}

// TestTaintConfig_WarmStartInvalidatedByConfig pins the WP-09 warm-start
// fingerprint: a store certified with NO project config must not warm-start once
// a .graphi/taint.json appears (the persisted findings would mean something
// different), and re-certifying under the config makes warm-start work again. A
// repo that never grows a config keeps warm-starting exactly as before.
func TestTaintConfig_WarmStartInvalidatedByConfig(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, customSinkRepo(false)) // no .graphi/taint.json yet

	dir := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()

	// A fresh Ingester per phase over the SAME meta sidecar simulates
	// "same store, new process".
	metaDir := t.TempDir()
	i1, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i1.Close()
	if err := i1.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if _, ok, err := i1.CanWarmStart(ctx, repo); err != nil || !ok {
		t.Fatalf("no-config repo must warm-start: ok=%v err=%v", ok, err)
	}

	// Drop a project config in; same meta, new process → stamp mismatch → cold.
	if err := os.MkdirAll(filepath.Join(repo, taint.ConfigDir), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, taint.ConfigDir, taint.ConfigFile), []byte(customSinkConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	i2, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i2.Close()
	if _, ok, err := i2.CanWarmStart(ctx, repo); err != nil || ok {
		t.Fatalf("added taint config must NOT warm-start: ok=%v err=%v", ok, err)
	}

	// Re-certify under the config, then warm-start works again.
	if err := i2.IngestAll(ctx, repo); err != nil {
		t.Fatalf("re-IngestAll: %v", err)
	}
	if _, ok, err := i2.CanWarmStart(ctx, repo); err != nil || !ok {
		t.Fatalf("re-certified config must warm-start: ok=%v err=%v", ok, err)
	}
}

// TestTaintConfig_MalformedFailsIngest verifies a broken project config fails
// the ingest closed rather than silently indexing with defaults — a security
// consumer must never get a false "clean" from a config typo.
func TestTaintConfig_MalformedFailsIngest(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	files := customSinkRepo(true)
	files[".graphi/taint.json"] = `{ not valid json`
	repo := writeRepo(t, files)
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err == nil {
		t.Fatal("ingest accepted a malformed .graphi/taint.json; want a hard failure (fail-closed)")
	}
}
