package client

// Adversarial contract §2 pins for the shared trust-report composition,
// complementing trust_report_test.go: a fixture seeded with a REAL
// parse-skipped file proves the details lists stay bounded, repo-relative and
// capped (rules 8–9 — no absolute private paths anywhere in the document); an
// unresolvable target WITHOUT a policy proves resolver findings reach the wire
// even when no policy adopts them (scope laundering guard); and a store-less
// repository with details requested proves every list is [] and never null
// (rules 1–2 hold on the fail-closed UNAVAILABLE document too).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
)

// trustClosedStates is the frozen §1.6 snapshot-state set.
var trustClosedStates = map[trust.State]bool{
	trust.StateCurrent:     true,
	trust.StateStale:       true,
	trust.StateIncomplete:  true,
	trust.StateUnavailable: true,
}

// buildParseSkipFixture is buildTrustFixture plus one malformed JSON file
// (Handlebars-templated, rejected by strict encoding/json), so the snapshot
// carries a genuine parse-skip fact with its bounded path sample.
func buildParseSkipFixture(t *testing.T) (root, dbPath, metaDir string) {
	t.Helper()
	ctx := context.Background()

	root = t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/fix\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		"main.go":      "package main\n\nimport \"example.com/fix/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
		"bad.json":     "{\n  {{#each xs}}\n  \"id\": \"x\"\n  {{/each}}\n}\n",
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	metaDir = t.TempDir()
	dbPath = filepath.Join(t.TempDir(), "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("close ingester: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return root, dbPath, metaDir
}

// TestTrustReport_ParseSkipDetailsStayRelativeAndCapped seeds a real
// parse-skipped file and attacks contract §2 rules 8–9: the skip is counted
// (files_skipped ≥ 1, files_discovered = indexed + skipped), the details
// sample names it repo-relative, --limit caps every details list, and NO
// absolute path — in particular the fixture root — appears anywhere in the
// document bytes, details or not.
func TestTrustReport_ParseSkipDetailsStayRelativeAndCapped(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildParseSkipFixture(t)
	d := NewDirect(nil, nil)

	b, _, state, err := d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Details: true,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if !trustClosedStates[state] {
		t.Fatalf("state %q is outside the closed §1.6 set", state)
	}
	if bytes.Contains(b, []byte(root)) {
		t.Fatalf("the document leaks the absolute repository root %q:\n%s", root, b)
	}

	doc := decodeTrustDoc(t, b)
	if got := string(doc["snapshot_state"]); !trustClosedStates[trust.State(strings.Trim(got, `"`))] {
		t.Errorf("snapshot_state = %s is outside the closed set", got)
	}
	var cov trustReportCoverage
	if err := json.Unmarshal(doc["coverage"], &cov); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if cov.FilesSkipped < 1 {
		t.Errorf("files_skipped = %d, want >= 1 (the malformed JSON must be counted, never hidden)", cov.FilesSkipped)
	}
	if cov.FilesDiscovered != cov.FilesIndexed+cov.FilesSkipped {
		t.Errorf("files_discovered = %d, want indexed(%d) + skipped(%d)", cov.FilesDiscovered, cov.FilesIndexed, cov.FilesSkipped)
	}
	var det trustReportDetails
	if err := json.Unmarshal(doc["details"], &det); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if len(det.ParsePaths) == 0 {
		t.Fatal("details.parse_paths is empty although a file was parse-skipped")
	}
	for _, p := range det.ParsePaths {
		if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || strings.Contains(p, root) {
			t.Errorf("details.parse_paths carries a non-relative path %q (rule 9)", p)
		}
	}
	sawBad := false
	for _, p := range det.ParsePaths {
		if p == "bad.json" {
			sawBad = true
		}
	}
	if !sawBad {
		t.Errorf("details.parse_paths %v misses the repo-relative skipped file", det.ParsePaths)
	}

	// The caller's limit caps every details list.
	b, _, _, err = d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Details: true, Limit: 1,
	})
	if err != nil {
		t.Fatalf("TrustReport(limit=1): %v", err)
	}
	if err := json.Unmarshal(decodeTrustDoc(t, b)["details"], &det); err != nil {
		t.Fatalf("decode limited details: %v", err)
	}
	if len(det.ParsePaths) > 1 || len(det.DegradedUnits) > 1 || len(det.TopBoundaries) > 1 {
		t.Errorf("limit=1 not enforced on details lists: %d/%d/%d entries",
			len(det.ParsePaths), len(det.DegradedUnits), len(det.TopBoundaries))
	}

	// Without the explicit request the details lists stay empty (rule 8), and
	// the skip COUNT still shows — details opt-in bounds evidence samples,
	// never the facts.
	b, _, _, err = d.TrustReport(ctx, TrustReportOptions{Root: root, DBPath: dbPath, MetaDir: metaDir})
	if err != nil {
		t.Fatalf("TrustReport(no details): %v", err)
	}
	doc = decodeTrustDoc(t, b)
	if err := json.Unmarshal(doc["details"], &det); err != nil {
		t.Fatalf("decode default details: %v", err)
	}
	if len(det.ParsePaths) != 0 || len(det.DegradedUnits) != 0 || len(det.TopBoundaries) != 0 {
		t.Errorf("details lists filled without the explicit request: %+v", det)
	}
	if err := json.Unmarshal(doc["coverage"], &cov); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if cov.FilesSkipped < 1 {
		t.Errorf("files_skipped = %d without details, want >= 1 (counts are facts, not details)", cov.FilesSkipped)
	}
}

// TestTrustReport_NoPolicyKeepsResolverFindings attacks scope laundering on
// the no-policy path: even when no policy adopts them, the resolver's
// fail-closed findings for an unresolvable target must reach the wire
// document — a target that resolved to nothing must never look like a clean
// repository-default report.
func TestTrustReport_NoPolicyKeepsResolverFindings(t *testing.T) {
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil)

	b, verdict, _, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Target: "no_such_symbol_xyz",
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if verdict != "" {
		t.Fatalf("verdict = %q, want the zero Verdict when no policy was requested", verdict)
	}
	doc := decodeTrustDoc(t, b)
	codes := docFindingCodes(t, doc)
	found := false
	for _, c := range codes {
		if c == trust.FindingTargetNotFound {
			found = true
		}
	}
	if !found {
		t.Fatalf("TARGET_NOT_FOUND dropped from the no-policy document; findings = %v", codes)
	}
	var scope trustReportScope
	if err := json.Unmarshal(doc["scope"], &scope); err != nil {
		t.Fatalf("decode scope: %v", err)
	}
	if scope.Kind != trust.ScopeSymbol || scope.ID != "" {
		t.Errorf("scope = %+v, want the visibly unresolved symbol scope (empty id)", scope)
	}
}

// TestTrustReport_UnavailableDetailsAreEmptyArrays attacks rules 1–2 on the
// fail-closed document: a store-less repository with details+limit requested
// still carries every register property, and every list — including the three
// details lists — encodes as [], never null.
func TestTrustReport_UnavailableDetailsAreEmptyArrays(t *testing.T) {
	d := NewDirect(nil, nil)
	b, _, state, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root:    t.TempDir(),
		DBPath:  filepath.Join(t.TempDir(), "never-built.db"),
		MetaDir: filepath.Join(t.TempDir(), "never-built-meta"),
		Details: true,
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if state != trust.StateUnavailable {
		t.Fatalf("state = %s, want UNAVAILABLE", state)
	}
	if bytes.Contains(b, []byte("null")) {
		t.Fatalf("the UNAVAILABLE document contains a null value:\n%s", b)
	}
	doc := decodeTrustDoc(t, b)
	for _, key := range trustReportRegisterKeys {
		if _, ok := doc[key]; !ok {
			t.Errorf("register property %q missing from the UNAVAILABLE+details document", key)
		}
	}
	var det map[string]json.RawMessage
	if err := json.Unmarshal(doc["details"], &det); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	for _, key := range []string{"parse_paths", "degraded_units", "top_boundaries"} {
		raw := bytes.TrimSpace(det[key])
		if !bytes.Equal(raw, []byte("[]")) {
			t.Errorf("details.%s = %s, want [] (empty arrays, never null)", key, raw)
		}
	}
}
