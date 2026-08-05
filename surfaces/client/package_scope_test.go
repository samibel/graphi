package client

// End-to-end pins for package assessment (PRD §22) over a REAL ingested
// repository. The engine-level tests (engine/trust/package_scope_test.go)
// prove the resolver and the rules; these prove the WIRING — that a real
// package key confirms against the real sidecar, that its real row reaches the
// policy, and that the two failure directions stay fail-closed.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
)

// buildPackageFixture writes a two-package Go repository and ingests it, so
// real trust_package_evidence rows exist for "." and "util".
func buildPackageFixture(t *testing.T) (root, dbPath, metaDir string) {
	t.Helper()
	ctx := context.Background()
	root = t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/pkgfix\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		"main.go":      "package main\n\nimport \"example.com/pkgfix/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
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

func decodeScope(t *testing.T, b []byte) (trustReportScope, trust.ScopeFacts) {
	t.Helper()
	var doc struct {
		Scope         trustReportScope `json:"scope"`
		ScopeEvidence trust.ScopeFacts `json:"scope_evidence"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	return doc.Scope, doc.ScopeEvidence
}

// TestPackageScope_ResolvesAgainstRealEvidence is the feature end to end: a
// real package directory resolves and its real row reaches the document.
func TestPackageScope_ResolvesAgainstRealEvidence(t *testing.T) {
	root, dbPath, metaDir := buildPackageFixture(t)
	d := NewDirect(nil, nil)

	b, verdict, state, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "util", Policy: trust.PolicyIDReview,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT", state)
	}
	scope, facts := decodeScope(t, b)
	if scope.Kind != trust.ScopePackage {
		t.Errorf("scope.kind = %q, want package", scope.Kind)
	}
	if scope.ID != "util" {
		t.Errorf("scope.id = %q, want util — a confirmed package carries its key", scope.ID)
	}
	if !facts.Available {
		t.Fatalf("scope_evidence.available = false; the row that CONFIRMED the package must also be readable\n%s", b)
	}
	if facts.Package.State == "" {
		t.Errorf("package claim is empty: %+v", facts.Package)
	}
	// A cleanly type-checked package is assessable — the whole point of the
	// feature. Before it, every package target read UNVERIFIED.
	if verdict == trust.VerdictUnverified {
		t.Errorf("verdict = UNVERIFIED for a real, cleanly checked package — the package is assessable now\n%s", b)
	}
}

// TestPackageScope_UnknownKeyStaysUnresolved is the fail-closed half over the
// real sidecar: a directory that is not a package must not resolve just
// because it looks like a path.
func TestPackageScope_UnknownKeyStaysUnresolved(t *testing.T) {
	root, dbPath, metaDir := buildPackageFixture(t)
	d := NewDirect(nil, nil)

	b, verdict, _, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir,
		Target: "internal/does/not/exist", Policy: trust.PolicyIDReview,
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	scope, facts := decodeScope(t, b)
	if scope.ID != "" {
		t.Errorf("scope.id = %q, want empty for an unknown key", scope.ID)
	}
	if facts.Available {
		t.Errorf("scope_evidence.available = true for an unknown package")
	}
	if verdict != trust.VerdictUnverified {
		t.Errorf("verdict = %q, want UNVERIFIED for an unresolvable target", verdict)
	}
}

// TestPackageScope_RootPackageResolves pins the "." key space explicitly. The
// repository root is a real package unit and its key is "." — a string no
// path-shaped heuristic would ever have guessed.
func TestPackageScope_RootPackageResolves(t *testing.T) {
	root, dbPath, metaDir := buildPackageFixture(t)
	d := NewDirect(nil, nil)

	b, _, _, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Target: ".",
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	scope, facts := decodeScope(t, b)
	if scope.Kind != trust.ScopePackage || scope.ID != "." {
		t.Errorf("scope = %+v, want a resolved package scope for \".\"", scope)
	}
	if !facts.Available {
		t.Errorf("root package row not readable\n%s", b)
	}
}

// TestPackageScope_FileTargetIsUnaffected pins that adding package resolution
// changed nothing for the targets that already worked: a file target still
// reads its FILE row and is never re-interpreted as a package.
//
// It deliberately does NOT assert scope.kind == "file". Ingest commits a node
// of kind "file" whose qualified name IS the path, so a path target matches on
// qualified name before the source-path branch runs and comes back as a
// resolved SYMBOL scope pointing at the file node. That predates this change
// and is harmless — the evidence fetched is the file's own row either way —
// so pinning the kind here would freeze an incidental resolution order rather
// than the property under test.
func TestPackageScope_FileTargetIsUnaffected(t *testing.T) {
	root, dbPath, metaDir := buildPackageFixture(t)
	d := NewDirect(nil, nil)

	b, _, _, err := d.TrustReport(context.Background(), TrustReportOptions{
		Root: root, DBPath: dbPath, MetaDir: metaDir, Target: "util/util.go",
	})
	if err != nil {
		t.Fatalf("TrustReport: %v", err)
	}
	scope, facts := decodeScope(t, b)
	if scope.Kind == trust.ScopePackage {
		t.Errorf("a file target was re-interpreted as a package: %+v", scope)
	}
	if !facts.Available || facts.File.ParseStatus == "" {
		t.Errorf("file target lost its file claim: %+v", facts)
	}
	if facts.Package.State != "" {
		t.Errorf("file target gained a package claim it never had: %+v", facts.Package)
	}
}
