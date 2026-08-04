package client

// Integration-style tests for the shared trust-report composition (P1 Labs):
// a tiny fixture repository gets a REAL full ingest (SQLite store + sidecar,
// the same auto-managed shape `graphi sync` produces), then the Direct facade
// composes the contract §2 document over it. Pinned here: the §2 field
// register with its always-present/empty-arrays rules, the fail-closed
// UNAVAILABLE document for a store-less repository (a valid document, never an
// error), the typed unknown-policy error, byte determinism (the parity
// property both surfaces inherit), and the resolver-findings pass-through for
// an unresolvable target.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
)

// buildTrustFixture writes a tiny Go repository and runs one full ingest into
// a durable SQLite store + sidecar meta dir — the exact state the trust-report
// composition observes read-only. The fixture mirrors the engine/ingest trust
// tests: a cross-package call the type resolver confirms, no parse skips, no
// degraded units, so the derived state is CURRENT.
func buildTrustFixture(t *testing.T) (root, dbPath, metaDir string) {
	t.Helper()
	ctx := context.Background()

	root = t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/fix\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		"main.go":      "package main\n\nimport \"example.com/fix/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
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

// decodeTrustDoc splits the document into raw top-level properties so tests
// can assert presence and array-ness without re-modelling the shape.
func decodeTrustDoc(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("document is not valid JSON: %v\n%s", err, b)
	}
	return doc
}

// docFindingCodes decodes the additive findings list into its codes.
func docFindingCodes(t *testing.T, doc map[string]json.RawMessage) []string {
	t.Helper()
	var findings []trust.Finding
	if err := json.Unmarshal(doc["findings"], &findings); err != nil {
		t.Fatalf("decode findings: %v", err)
	}
	codes := make([]string, 0, len(findings))
	for _, f := range findings {
		codes = append(codes, f.Code)
	}
	return codes
}

// trustReportRegisterKeys is the frozen §2.2 field register plus the four
// documented additive v1 fields, in no particular order (presence is the rule
// under test; order is pinned by the determinism test via raw bytes).
var trustReportRegisterKeys = []string{
	"schema_version", "snapshot_version", "snapshot_state", "graph_generation",
	"freshness", "scope", "coverage", "edge_evidence", "resolution",
	"boundaries", "policy", "limitations",
	"findings", "checks_passed", "details", "scope_evidence",
}

// TestTrustReport_NoPolicyDocument is spot-check (a): over a freshly ingested
// fixture, the no-policy document carries every register property, arrays are
// [] (never null), the policy object is its zero-value presence, and the
// derived snapshot state is CURRENT.
func TestTrustReport_NoPolicyDocument(t *testing.T) {
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil) // the composition is self-contained: no wiring needed

	b, verdict, state, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT after an uninterrupted full pass", state)
	}
	if verdict != "" {
		t.Fatalf("verdict = %q, want the zero Verdict when no policy was requested", verdict)
	}

	doc := decodeTrustDoc(t, b)
	for _, key := range trustReportRegisterKeys {
		if _, ok := doc[key]; !ok {
			t.Errorf("register property %q missing from the document", key)
		}
	}
	if len(doc) != len(trustReportRegisterKeys) {
		t.Errorf("document has %d top-level properties, want %d (additions are contract decisions, not drift)", len(doc), len(trustReportRegisterKeys))
	}

	// Always-present zero values, never omission (§2.3 rules 1–2).
	if got := string(doc["snapshot_state"]); got != `"CURRENT"` {
		t.Errorf("snapshot_state = %s, want \"CURRENT\"", got)
	}
	if got := string(doc["policy"]); got != `{"name":"","version":0,"verdict":""}` {
		t.Errorf("policy zero-value presence broken: %s", got)
	}
	for _, key := range []string{"findings", "limitations", "checks_passed"} {
		raw := bytes.TrimSpace(doc[key])
		if !bytes.HasPrefix(raw, []byte("[")) {
			t.Errorf("%s = %s, want a JSON array (empty arrays, never null)", key, raw)
		}
		if bytes.Equal(raw, []byte("null")) {
			t.Errorf("%s serialized as null", key)
		}
	}

	// The closed tier keys carry the fixture's evidence (the typeresolve-
	// confirmed cross-package call).
	var tiers map[string]int
	if err := json.Unmarshal(doc["edge_evidence"], &tiers); err != nil {
		t.Fatalf("decode edge_evidence: %v", err)
	}
	for _, tier := range []string{"confirmed", "derived", "heuristic"} {
		if _, ok := tiers[tier]; !ok {
			t.Errorf("edge_evidence misses the frozen tier key %q", tier)
		}
	}
	if len(tiers) != 3 {
		t.Errorf("edge_evidence has %d keys, want exactly the three frozen tiers", len(tiers))
	}
	if tiers["confirmed"] == 0 {
		t.Error("fixture's typeresolve-confirmed edge is missing from edge_evidence")
	}

	// The standing structural boundaries are always present ({code, severity,
	// count} — the §2.5 vocabulary).
	var bounds []trustReportBoundary
	if err := json.Unmarshal(doc["boundaries"], &bounds); err != nil {
		t.Fatalf("decode boundaries: %v", err)
	}
	got := map[string]bool{}
	for _, b := range bounds {
		got[b.Code] = true
	}
	for _, code := range []string{trust.LimitationCrossRepositoryUnavailable, trust.LimitationDependencyInternalsUnknown} {
		if !got[code] {
			t.Errorf("standing boundary %s missing from boundaries", code)
		}
	}
}

// TestTrustReport_UnknownPolicy is (b): a name outside the built-in registry
// is the typed operational error trust.ErrPolicyUnknown (contract §4 /
// ErrTrustPolicyUnknown; CLI exit 2), decided before any store I/O.
func TestTrustReport_UnknownPolicy(t *testing.T) {
	d := NewDirect(nil, nil)
	_, _, _, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: t.TempDir(), Policy: "certainly-not-a-policy",
	})
	if !errors.Is(err, trust.ErrPolicyUnknown) {
		t.Fatalf("err = %v, want trust.ErrPolicyUnknown", err)
	}
}

// TestTrustReport_NoStoreUnavailable is (c): a repository with no graph store
// still composes a VALID document — snapshot_state UNAVAILABLE, zero counts,
// arrays empty — never an error (fail closed, ADR 0006: the failure direction
// is "no answer"). With a policy attached, missing evidence maps to UNKNOWN,
// never PASS.
func TestTrustReport_NoStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	d := NewDirect(nil, nil)
	opts := TrustReportOptions{
		Root:    t.TempDir(),
		DBPath:  filepath.Join(t.TempDir(), "never-built.db"),
		MetaDir: filepath.Join(t.TempDir(), "never-built-meta"),
	}

	b, verdict, state, err := d.TrustReport(ctx, opts)
	if err != nil {
		t.Fatalf("TrustReport over a store-less repo must not error, got %v", err)
	}
	if state != trust.StateUnavailable {
		t.Fatalf("state = %s, want UNAVAILABLE", state)
	}
	if verdict != "" {
		t.Fatalf("verdict = %q, want the zero Verdict when no policy was requested", verdict)
	}
	doc := decodeTrustDoc(t, b)
	for _, key := range trustReportRegisterKeys {
		if _, ok := doc[key]; !ok {
			t.Errorf("register property %q missing from the UNAVAILABLE document", key)
		}
	}
	if got := string(doc["snapshot_state"]); got != `"UNAVAILABLE"` {
		t.Errorf("snapshot_state = %s, want \"UNAVAILABLE\"", got)
	}

	// Fail closed under a policy: no evidence never reads PASS.
	opts.Policy = trust.PolicyNameReview
	_, verdict, state, err = d.TrustReport(ctx, opts)
	if err != nil {
		t.Fatalf("TrustReport(review) over a store-less repo must not error, got %v", err)
	}
	if state != trust.StateUnavailable || verdict != trust.VerdictUnknown {
		t.Fatalf("(state, verdict) = (%s, %s), want (UNAVAILABLE, UNKNOWN)", state, verdict)
	}
}

// TestTrustReport_Deterministic is (d): identical facts yield byte-identical
// documents — the parity property every surface inherits from the single
// composition + single encoder.
func TestTrustReport_Deterministic(t *testing.T) {
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil)
	opts := TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Policy: trust.PolicyNameExploratory, Details: true, Limit: 3,
	}

	first, _, _, err := d.TrustReport(context.Background(), opts)
	if err != nil {
		t.Fatalf("TrustReport (first): %v", err)
	}
	second, _, _, err := d.TrustReport(context.Background(), opts)
	if err != nil {
		t.Fatalf("TrustReport (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two compositions over identical facts differ:\n%s\n---\n%s", first, second)
	}
}

// trustDocScopeEvidence decodes the additive scope_evidence object.
func trustDocScopeEvidence(t *testing.T, doc map[string]json.RawMessage) trust.ScopeFacts {
	t.Helper()
	var se trust.ScopeFacts
	if err := json.Unmarshal(doc["scope_evidence"], &se); err != nil {
		t.Fatalf("decode scope_evidence: %v", err)
	}
	return se
}

// TestTrustReport_ScopeEvidenceFileTarget pins the decidable target scope
// (US-3: local findings must not drown in the repository aggregate and vice
// versa): for a resolved file target the composition fetches the file's
// persisted evidence row under the snapshot generation, surfaces it as the
// always-present scope_evidence object, and hands it to the policy — so the
// clean file PASSes automated_change (with the explicit checks list, no
// SCOPE_EVIDENCE_UNAVAILABLE) while the file with unresolved references FAILs
// with UNRESOLVED_REFERENCE_IN_SCOPE, over the SAME snapshot. The fixture
// rows are pinned upstream (engine/ingest trust_evidence_test.go):
// util/util.go is clean, main.go carries skipped references.
func TestTrustReport_ScopeEvidenceFileTarget(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil)

	// Clean file: scoped PASS with the evidence row on the wire.
	b, verdict, state, err := d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "util/util.go", Policy: trust.PolicyNameAutomatedChange,
	})
	if err != nil {
		t.Fatalf("TrustReport(util/util.go): %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT", state)
	}
	if verdict != trust.VerdictPass {
		t.Fatalf("automated_change over the clean file scope = %s, want PASS (scope facts make the scope decidable)\n%s", verdict, b)
	}
	doc := decodeTrustDoc(t, b)
	se := trustDocScopeEvidence(t, doc)
	if !se.Available || se.File.ParseStatus != trust.ScopeParseStatusParsed {
		t.Errorf("scope_evidence = %+v, want available with a parsed file row", se)
	}
	for _, code := range docFindingCodes(t, doc) {
		if code == trust.FindingScopeEvidenceUnavailable {
			t.Error("SCOPE_EVIDENCE_UNAVAILABLE fired although the file's evidence row was fetched")
		}
	}
	var checks []string
	if err := json.Unmarshal(doc["checks_passed"], &checks); err != nil {
		t.Fatalf("decode checks_passed: %v", err)
	}
	if len(checks) == 0 {
		t.Error("scoped PASS without the explicit all-checks-passed list (PRD §26)")
	}

	// Dirty file, same snapshot: the row's skipped counter FAILs the policy.
	b, verdict, _, err = d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "main.go", Policy: trust.PolicyNameAutomatedChange,
	})
	if err != nil {
		t.Fatalf("TrustReport(main.go): %v", err)
	}
	if verdict != trust.VerdictFail {
		t.Fatalf("automated_change over the unresolved-refs file = %s, want FAIL\n%s", verdict, b)
	}
	doc = decodeTrustDoc(t, b)
	se = trustDocScopeEvidence(t, doc)
	if !se.Available || se.File.Skipped == 0 {
		t.Errorf("scope_evidence = %+v, want available with the nonzero skipped counter", se)
	}
	sawUnresolved := false
	for _, code := range docFindingCodes(t, doc) {
		if code == trust.FindingUnresolvedReferenceInScope {
			sawUnresolved = true
		}
	}
	if !sawUnresolved {
		t.Errorf("UNRESOLVED_REFERENCE_IN_SCOPE missing from findings: %v", docFindingCodes(t, doc))
	}

	// No policy: the facts are still fetched and surfaced (facts and policy
	// stay separate — the object is evidence, not a verdict input only).
	b, _, _, err = d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Target: "util/util.go",
	})
	if err != nil {
		t.Fatalf("TrustReport(no policy): %v", err)
	}
	if se := trustDocScopeEvidence(t, decodeTrustDoc(t, b)); !se.Available {
		t.Errorf("scope_evidence without a policy = %+v, want the fetched row", se)
	}

	// Unresolvable target: the object stays present, zero-valued, available
	// false — absence is visible, never dressed up (fail closed).
	b, _, _, err = d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Target: "no_such_symbol_xyz",
	})
	if err != nil {
		t.Fatalf("TrustReport(unresolvable): %v", err)
	}
	if se := trustDocScopeEvidence(t, decodeTrustDoc(t, b)); se.Available {
		t.Errorf("scope_evidence for an unresolved target = %+v, want available=false", se)
	}
}

// TestTrustReport_TargetNotFound is (e): an unresolvable target keeps the
// resolver's TARGET_NOT_FOUND finding in the document (the pass-through the
// false-pass pins guard — findings are never dropped between resolver and
// policy) and maps to the policy's table verdict: UNKNOWN for review, FAIL for
// automated_change.
func TestTrustReport_TargetNotFound(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil)

	cases := []struct {
		policy string
		want   trust.Verdict
	}{
		{trust.PolicyNameReview, trust.VerdictUnknown},
		{trust.PolicyNameAutomatedChange, trust.VerdictFail},
	}
	for _, tc := range cases {
		b, verdict, state, err := d.TrustReport(ctx, TrustReportOptions{
			Root: root, DBPath: dbPath, MetaDir: metaDir,
			Target: "no_such_symbol_xyz", Policy: tc.policy,
		})
		if err != nil {
			t.Fatalf("TrustReport(%s): %v", tc.policy, err)
		}
		if state != trust.StateCurrent {
			t.Errorf("%s: state = %s, want CURRENT (the graph itself is fine)", tc.policy, state)
		}
		if verdict != tc.want {
			t.Errorf("%s: verdict = %s, want %s", tc.policy, verdict, tc.want)
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
			t.Errorf("%s: TARGET_NOT_FOUND missing from document findings %v", tc.policy, codes)
		}
		var pol trustReportPolicy
		if err := json.Unmarshal(doc["policy"], &pol); err != nil {
			t.Fatalf("decode policy: %v", err)
		}
		if pol.Name != tc.policy || pol.Version != 1 || pol.Verdict != tc.want {
			t.Errorf("%s: policy object = %+v, want {%s 1 %s}", tc.policy, pol, tc.policy, tc.want)
		}
		var scope trustReportScope
		if err := json.Unmarshal(doc["scope"], &scope); err != nil {
			t.Fatalf("decode scope: %v", err)
		}
		if scope.Kind != trust.ScopeSymbol || scope.ID != "" {
			t.Errorf("%s: scope = %+v, want the visibly unresolved symbol scope (empty id)", tc.policy, scope)
		}
	}
}
