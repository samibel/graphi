package client

// Adversarial pins for the scope_evidence path of the trust-report composition
// (trust_report.go scopeEvidenceFacts): stale or rebound evidence rows must
// never reach ScopeFacts, and the wire object stays deterministic and
// path-clean. All attacks were held by the production code; each is pinned
// here as a regression gate:
//
//   - Rows whose generation no longer matches the snapshot's (an older pass's
//     leftovers, or any tampering that moved the generation column) read
//     not-found: scope_evidence.available=false and the policy fires
//     SCOPE_EVIDENCE_UNAVAILABLE — never zero-valued "clean" facts.
//   - Rebinding the store's generation keys (live + snapshot-generation)
//     around fabricated clean rows cannot mint a CURRENT state (the
//     digest-protected inner binding catches the rewrite) and cannot route
//     the fabricated rows into ScopeFacts (the lookup keys on the inner,
//     digest-protected snapshot generation).
//   - The document with a resolved target is byte-deterministic, never
//     carries an absolute path, and scope_evidence is always the full object
//     (available=false is visible absence, not omission or null).

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/trust"
)

// openSidecar opens the fixture's ingest-meta.db read-write for row tampering.
func openSidecar(t *testing.T, metaDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db"))
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestTrustReport_StaleGenerationEvidenceNeverServed — the stale-row attack:
// move every file-evidence row to another generation (an older pass's
// leftovers around a generation rebinding). The graph itself is untouched
// (state stays CURRENT), but the scope lookup must read not-found: available
// stays false, the policy falls back to SCOPE_EVIDENCE_UNAVAILABLE, and the
// verdict is UNKNOWN — never a PASS minted from rows of another pass and never
// zero-valued clean facts.
func TestTrustReport_StaleGenerationEvidenceNeverServed(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildTrustFixture(t)
	db := openSidecar(t, metaDir)
	if _, err := db.ExecContext(ctx, "UPDATE trust_file_evidence SET generation_id = 'older-generation'"); err != nil {
		t.Fatalf("rebind evidence rows: %v", err)
	}

	d := NewDirect(nil, nil)
	b, verdict, state, err := d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "util/util.go", Policy: trust.PolicyIDAutomatedChange,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT (the graph and snapshot are untouched)", state)
	}
	if verdict == trust.VerdictPass {
		t.Fatalf("FALSE PASS: rows of another generation were laundered into a scoped PASS\n%s", b)
	}
	if verdict != trust.VerdictUnverified {
		t.Errorf("verdict = %s, want UNKNOWN (scope evidence absent, A10)", verdict)
	}
	doc := decodeTrustDoc(t, b)
	if se := trustDocScopeEvidence(t, doc); se.Available {
		t.Errorf("scope_evidence = %+v, want available=false — a row of another generation is stale evidence", se)
	}
	sawUnavailable := false
	for _, code := range docFindingCodes(t, doc) {
		if code == trust.FindingScopeEvidenceUnavailable {
			sawUnavailable = true
		}
	}
	if !sawUnavailable {
		t.Errorf("SCOPE_EVIDENCE_UNAVAILABLE missing; findings = %v", docFindingCodes(t, doc))
	}
}

// TestTrustReport_GenerationRebindingAroundFabricatedRows — the rebinding
// attack: delete the real evidence rows, plant fabricated CLEAN rows under a
// foreign generation, and rebind BOTH store generation keys (live +
// snapshot-generation) to that foreign generation so the key-level equalities
// hold. The digest-protected inner binding must still read STALE (never
// CURRENT), the automated-change verdict must not be PASS, and the fabricated
// rows must never reach scope_evidence — the lookup keys on the snapshot's
// inner generation, which no key-level rewrite can move.
func TestTrustReport_GenerationRebindingAroundFabricatedRows(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildTrustFixture(t)

	// main.go's REAL row carries skipped references (pinned upstream); the
	// attacker's goal is to launder it into a clean row.
	db := openSidecar(t, metaDir)
	if _, err := db.ExecContext(ctx, "DELETE FROM trust_file_evidence"); err != nil {
		t.Fatalf("delete real rows: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO trust_file_evidence
		VALUES ('forged-generation', 'main.go', 'go', 'parsed', '', 0, 0, 0, 0, 0)`); err != nil {
		t.Fatalf("plant fabricated clean row: %v", err)
	}

	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	for _, key := range []string{liveGenerationKey, trust.MetaSnapshotGeneration} {
		if err := store.SetMetadata(ctx, key, "forged-generation"); err != nil {
			_ = store.Close()
			t.Fatalf("rebind %s: %v", key, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	d := NewDirect(nil, nil)
	b, verdict, state, err := d.TrustReport(ctx, TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "main.go", Policy: trust.PolicyIDAutomatedChange,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if state == trust.StateCurrent {
		t.Fatalf("FALSE GREEN: generation keys rebound around unrefreshed snapshot bytes read CURRENT\n%s", b)
	}
	if verdict == trust.VerdictPass {
		t.Fatalf("FALSE PASS: fabricated rows under a rebound generation laundered a PASS\n%s", b)
	}
	if se := trustDocScopeEvidence(t, decodeTrustDoc(t, b)); se.Available {
		t.Errorf("scope_evidence = %+v, want available=false — the fabricated foreign-generation row must never be served", se)
	}
}

// TestTrustReport_ScopeEvidenceDeterminismAndPathHygiene — contract rules on
// the scope_evidence path: with a resolved file target (+policy, +details) two
// compositions are byte-identical, the document never contains the absolute
// repository root or a null, and the scope_evidence object is always complete
// — available=true with the row for a resolved target, and the full
// zero-valued object (never omission) for an unresolvable one.
func TestTrustReport_ScopeEvidenceDeterminismAndPathHygiene(t *testing.T) {
	ctx := context.Background()
	root, dbPath, metaDir := buildTrustFixture(t)
	d := NewDirect(nil, nil)
	opts := TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "util/util.go", Policy: trust.PolicyIDAutomatedChange,
		Details: true, Limit: 3,
	}

	first, _, _, err := d.TrustReport(ctx, opts)
	if err != nil {
		t.Fatalf("TrustReport (first): %v", err)
	}
	second, _, _, err := d.TrustReport(ctx, opts)
	if err != nil {
		t.Fatalf("TrustReport (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two compositions with a target differ:\n%s\n---\n%s", first, second)
	}
	if bytes.Contains(first, []byte(root)) {
		t.Errorf("the document leaks the absolute repository root %q:\n%s", root, first)
	}
	if bytes.Contains(first, []byte("null")) {
		t.Errorf("the document contains a null value:\n%s", first)
	}
	if se := trustDocScopeEvidence(t, decodeTrustDoc(t, first)); !se.Available {
		t.Errorf("scope_evidence = %+v, want the resolved target's row (available=true)", se)
	}

	// Unresolvable target: the object stays the complete zero value on the
	// wire — every field present, available false, no null anywhere.
	opts.Target = "no_such_symbol_xyz"
	b, _, _, err := d.TrustReport(ctx, opts)
	if err != nil {
		t.Fatalf("TrustReport (unresolvable): %v", err)
	}
	if bytes.Contains(b, []byte("null")) {
		t.Errorf("the unresolved-target document contains a null value:\n%s", b)
	}
	if !bytes.Contains(b, []byte(`"scope_evidence":{"available":false,"file":{"parse_status":""`)) {
		t.Errorf("scope_evidence is not the complete zero-valued object:\n%s", b)
	}
}
