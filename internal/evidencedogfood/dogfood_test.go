// Package evidencedogfood holds the ONE test that points graphi at its own
// honesty gate.
//
// SW-205's AC-5 resolves a `path/to/file.go::SymbolName` citation with go/ast,
// because internal/evidence and cmd/evidence are stdlib-only by declared design
// and because an auditor that runs on the artifact it audits is wrong in the same
// direction as its subject. graphi's own index would have been the dogfooding
// choice; this package is how the dogfooding signal is kept without paying that
// price: it asserts that graphi's own Go parser AGREES with go/ast on every
// `::Symbol` citation the governed records actually make.
//
// It lives outside internal/evidence on purpose — importing core/parse from
// internal/evidence, even in a _test.go, would put a non-stdlib import in the
// package AC-14 requires to have none.
//
// The assertion is AGREEMENT, never a fixed verdict, so the test does not go red
// when the repository legitimately changes — which is the exact failure mode this
// story exists to fix, and re-creating it inside the suite would be a joke.
package evidencedogfood

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/evidence"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Skip("not inside a module")
	}
	return filepath.Dir(gomod)
}

// TestGraphiAgreesWithGoAST_OnGovernedSymbolCitations is the dogfooding
// cross-check: for every `file.go::Symbol` citation in the declared governed
// record set, graphi's parser and go/ast must give the same yes/no answer.
func TestGraphiAgreesWithGoAST_OnGovernedSymbolCitations(t *testing.T) {
	root := repoRoot(t)
	docs, err := evidence.ListGoverned(root)
	if err != nil {
		t.Fatalf("ListGoverned: %v", err)
	}

	reg := parse.NewDefaultRegistry()
	checked := 0
	for _, doc := range docs {
		text, err := evidence.ReadGoverned(root, doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		cites, _ := evidence.ScanDocument(doc, text)
		for _, c := range cites {
			if c.Kind != evidence.KindTestSymbol || !strings.HasSuffix(c.Path, ".go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Path)))
			if err != nil {
				continue // the file does not exist; that is AC-2's job, not this test's
			}
			want, err := evidence.DeclaredSymbols(c.Path, src)
			if err != nil {
				t.Fatalf("%s: go/ast could not parse %s: %v", doc, c.Path, err)
			}
			got := graphiDeclares(t, reg, c.Path, src)
			if want[c.Symbol] != got[c.Symbol] {
				t.Errorf("%s cites %s::%s — go/ast says declared=%v, graphi's own parser says declared=%v",
					doc, c.Path, c.Symbol, want[c.Symbol], got[c.Symbol])
			}
			checked++
		}
	}
	if checked == 0 {
		t.Skip("no Go symbol citations in the governed record set — nothing to cross-check")
	}
	t.Logf("cross-checked %d governed `file.go::Symbol` citation(s); graphi's parser and go/ast agree on all of them", checked)
}

// graphiDeclares asks graphi's own parser which symbols a file declares.
func graphiDeclares(t *testing.T, reg *parse.Registry, path string, src []byte) map[string]bool {
	t.Helper()
	p, err := reg.ParserFor(path)
	if err != nil {
		t.Fatalf("graphi has no parser for %s: %v", path, err)
	}
	res, err := p.Parse(context.Background(), path, src)
	if err != nil {
		t.Fatalf("graphi failed to parse %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, n := range res.Nodes {
		q := n.QualifiedName()
		out[q] = true
		if i := strings.LastIndexAny(q, ".#/"); i >= 0 && i+1 < len(q) {
			out[q[i+1:]] = true
		}
	}
	return out
}
